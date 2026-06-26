package dnsbench

import (
	"errors"
	"testing"
	"time"
)

func TestParseDomains(t *testing.T) {
	got := ParseDomains(" a.com , b.com ,a.com,, c.com ")
	want := []string{"a.com", "b.com", "c.com"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i].Name != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
		if got[i].Category != CategoryCustom {
			t.Fatalf("domain %q category = %q, want %q", got[i].Name, got[i].Category, CategoryCustom)
		}
	}
}

func TestParseDomainsEmpty(t *testing.T) {
	if got := ParseDomains("  , , "); len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
}

func TestRunQueriesEarlyAbort(t *testing.T) {
	domains := []Domain{{Name: "a.com"}, {Name: "b.com"}, {Name: "c.com"}}
	opts := Options{Domains: domains, Queries: 4} // 1 warm-up + 12 measured
	calls := 0
	q := func(string) (time.Duration, error) {
		calls++
		return time.Millisecond, errors.New("boom")
	}
	ch := make(chan queryResult, len(domains)*opts.Queries)
	runQueries(Server{Name: "X"}, q, opts, ch, func(string) {})
	close(ch)

	// Every measured query must still be reported so the progress UI reaches 100%.
	if got := len(ch); got != len(domains)*opts.Queries {
		t.Fatalf("emitted %d results, want %d", got, len(domains)*opts.Queries)
	}
	// The querier stops being called once the unreachable streak is reached:
	// warm-up (1) + (unreachableFailStreak-1) measured queries.
	if calls != unreachableFailStreak {
		t.Errorf("querier called %d times, want %d (early abort)", calls, unreachableFailStreak)
	}
}

func TestRunQueriesAllSuccess(t *testing.T) {
	domains := []Domain{{Name: "a.com"}, {Name: "b.com"}}
	opts := Options{Domains: domains, Queries: 3}
	calls := 0
	q := func(string) (time.Duration, error) { calls++; return time.Millisecond, nil }
	ch := make(chan queryResult, len(domains)*opts.Queries)
	runQueries(Server{Name: "X"}, q, opts, ch, func(string) {})
	close(ch)

	// A healthy server is never aborted: 1 warm-up + every measured query runs.
	if want := 1 + len(domains)*opts.Queries; calls != want {
		t.Errorf("querier called %d times, want %d", calls, want)
	}
}

func TestAggregateResults(t *testing.T) {
	srv := Server{Name: "X", Address: "1.2.3.4", Protocol: UDP}
	ch := make(chan queryResult, 3)
	ch <- queryResult{server: srv, duration: 10 * time.Millisecond}
	ch <- queryResult{server: srv, duration: 30 * time.Millisecond}
	ch <- queryResult{server: srv, err: errors.New("boom")}
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
	// After sorting, results should be in descending score order.
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

	// dead server: no successes, so score and avg latency should be 0.
	dead := res[len(res)-1]
	if dead.Score != 0 || dead.AvgTime != 0 || dead.SuccessRate != 0 {
		t.Fatalf("dead server should be all-zero, got %+v", dead)
	}

	// fast's avg latency should be 10ms (20ms / 2 successes).
	if res[0].AvgTime != 10*time.Millisecond {
		t.Fatalf("fast AvgTime=%v, want 10ms", res[0].AvgTime)
	}
}
