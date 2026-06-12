package main

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func TestParseDomains(t *testing.T) {
	got := parseDomains(" a.com , b.com ,a.com,, c.com ")
	want := []string{"a.com", "b.com", "c.com"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestParseDomainsEmpty(t *testing.T) {
	if got := parseDomains("  , , "); len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
}

func TestAggregateResults(t *testing.T) {
	srv := DNSServer{Name: "X", Address: "1.2.3.4", Protocol: UDP}
	ch := make(chan QueryResult, 3)
	ch <- QueryResult{Server: srv, Duration: 10 * time.Millisecond}
	ch <- QueryResult{Server: srv, Duration: 30 * time.Millisecond}
	ch <- QueryResult{Server: srv, Err: errors.New("boom")}
	close(ch)

	stats := aggregateResults(ch)
	s, ok := stats["X"]
	if !ok {
		t.Fatal("missing server X")
	}
	if s.total != 3 || s.successes != 2 {
		t.Fatalf("total=%d successes=%d, want 3/2", s.total, s.successes)
	}
	if s.totalTime != 40*time.Millisecond {
		t.Fatalf("totalTime=%v, want 40ms", s.totalTime)
	}
	if s.address != "1.2.3.4" {
		t.Fatalf("address=%q", s.address)
	}
}

func TestCalculateScores(t *testing.T) {
	stats := map[string]*serverStat{
		"fast":  {totalTime: 20 * time.Millisecond, successes: 2, total: 2, address: "1.1.1.1"},
		"slow":  {totalTime: 200 * time.Millisecond, successes: 2, total: 2, address: "2.2.2.2"},
		"flaky": {totalTime: 10 * time.Millisecond, successes: 1, total: 2, address: "3.3.3.3"},
		"dead":  {successes: 0, total: 2, address: "4.4.4.4"},
	}

	res := calculateScores(stats)
	if len(res) != 4 {
		t.Fatalf("expected 4 results, got %d", len(res))
	}
	// 排序后应为评分降序。
	for i := 1; i < len(res); i++ {
		if res[i-1].Score < res[i].Score {
			t.Fatalf("results not sorted by score desc: %+v", res)
		}
	}
	if res[0].Name != "fast" {
		t.Fatalf("expected fast first, got %q", res[0].Name)
	}
	if res[len(res)-1].Name != "dead" {
		t.Fatalf("expected dead last, got %q", res[len(res)-1].Name)
	}

	// dead 服务器：无成功，评分与平均延迟应为 0。
	dead := res[len(res)-1]
	if dead.Score != 0 || dead.AvgTime != 0 || dead.SuccessRate != 0 {
		t.Fatalf("dead server should be all-zero, got %+v", dead)
	}

	// fast 的平均延迟应为 10ms（20ms/2 成功）。
	if res[0].AvgTime != 10*time.Millisecond {
		t.Fatalf("fast AvgTime=%v, want 10ms", res[0].AvgTime)
	}
}

// startTestDNS 启动一个本地 UDP DNS 服务器，按 handler 应答，返回监听地址。
func startTestDNS(t *testing.T, handler dns.HandlerFunc) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &dns.Server{PacketConn: pc, Handler: handler}
	go srv.ActivateAndServe()
	t.Cleanup(func() { srv.Shutdown() })
	return pc.LocalAddr().String()
}

func TestReusableExchange(t *testing.T) {
	var calls atomic.Int32
	addr := startTestDNS(t, func(w dns.ResponseWriter, r *dns.Msg) {
		calls.Add(1)
		m := new(dns.Msg)
		m.SetReply(r)
		rr, _ := dns.NewRR(r.Question[0].Name + " 60 IN A 1.2.3.4")
		m.Answer = append(m.Answer, rr)
		w.WriteMsg(m)
	})

	client := &dns.Client{Net: "udp", Timeout: time.Second}
	q, closeFn := reusableExchange(client, addr)
	defer closeFn()

	for i := range 3 {
		d, err := q("example.com")
		if err != nil {
			t.Fatalf("query %d: %v", i, err)
		}
		if d <= 0 {
			t.Fatalf("query %d: non-positive duration %v", i, d)
		}
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("server received %d queries, want 3", got)
	}
}

func TestReusableExchangeServfail(t *testing.T) {
	addr := startTestDNS(t, func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Rcode = dns.RcodeServerFailure
		w.WriteMsg(m)
	})

	client := &dns.Client{Net: "udp", Timeout: time.Second}
	q, closeFn := reusableExchange(client, addr)
	defer closeFn()

	if _, err := q("example.com"); err == nil {
		t.Fatal("expected error for SERVFAIL rcode")
	}
}

func TestDohQuery(t *testing.T) {
	client := &http.Client{Timeout: time.Second}

	t.Run("valid", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"Status":0,"Answer":[{"data":"1.2.3.4"}]}`))
		}))
		defer srv.Close()
		if err := dohQuery(client, srv.URL, "example.com"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("nxdomain status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"Status":3}`))
		}))
		defer srv.Close()
		if err := dohQuery(client, srv.URL, "example.com"); err == nil {
			t.Fatal("expected error for non-zero status")
		}
	})

	t.Run("http error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()
		if err := dohQuery(client, srv.URL, "example.com"); err == nil {
			t.Fatal("expected error for HTTP 500")
		}
	})

	t.Run("garbage body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`not json`))
		}))
		defer srv.Close()
		if err := dohQuery(client, srv.URL, "example.com"); err == nil {
			t.Fatal("expected error for invalid JSON")
		}
	})
}
