package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/briandowns/spinner"
	"github.com/fatih/color"
	"github.com/miekg/dns"
	"github.com/olekukonko/tablewriter"
	"github.com/schollz/progressbar/v3"
	"github.com/spf13/cobra"
)

const (
	UDP = "udp"
	DOT = "dot"
	DOH = "doh"
)

type DNSServer struct{ Name, Address, Protocol string }

type QueryResult struct {
	Server   DNSServer
	Domain   string
	Duration time.Duration
	Err      error
}

type BenchmarkResult struct {
	Name, Address      string
	AvgTime            time.Duration
	SuccessRate, Score float64
	Successes, Total   int
}

// serverStat 聚合单个 DNS 服务器的测试数据。
type serverStat struct {
	totalTime time.Duration
	successes int
	total     int
	address   string
}

var (
	dnsServers = []DNSServer{
		{Name: "AliDNS 1 (UDP)", Address: "223.5.5.5", Protocol: UDP},
		{Name: "AliDNS 2 (UDP)", Address: "223.6.6.6", Protocol: UDP},
		{Name: "BaiduDNS (UDP)", Address: "180.76.76.76", Protocol: UDP},
		{Name: "DNSPod 1 (UDP)", Address: "119.28.28.28", Protocol: UDP},
		{Name: "DNSPod 2 (UDP)", Address: "119.29.29.29", Protocol: UDP},
		{Name: "114DNS 1 (UDP)", Address: "114.114.114.114", Protocol: UDP},
		{Name: "114DNS 2 (UDP)", Address: "114.114.115.115", Protocol: UDP},
		{Name: "114DNS Safe 1 (UDP)", Address: "114.114.114.119", Protocol: UDP},
		{Name: "114DNS Safe 2 (UDP)", Address: "114.114.115.119", Protocol: UDP},
		{Name: "114DNS Family 1 (UDP)", Address: "114.114.114.110", Protocol: UDP},
		{Name: "114DNS Family 2 (UDP)", Address: "114.114.115.110", Protocol: UDP},
		{Name: "Bytedance 1 (UDP)", Address: "180.184.1.1", Protocol: UDP},
		{Name: "Bytedance 2 (UDP)", Address: "180.184.2.2", Protocol: UDP},
		{Name: "Google 1 (UDP)", Address: "8.8.8.8", Protocol: UDP},
		{Name: "Google 2 (UDP)", Address: "8.8.4.4", Protocol: UDP},
		{Name: "Cloudflare 1 (UDP)", Address: "1.1.1.1", Protocol: UDP},
		{Name: "Cloudflare 2 (UDP)", Address: "1.0.0.1", Protocol: UDP},
		{Name: "Freenom 1 (UDP)", Address: "80.80.80.80", Protocol: UDP},
		{Name: "Freenom 2 (UDP)", Address: "80.80.81.81", Protocol: UDP},

		{Name: "AliDNS (DoT)", Address: "dns.alidns.com", Protocol: DOT},
		{Name: "DNSPod (DoT)", Address: "dot.pub", Protocol: DOT},
		{Name: "Google (DoT)", Address: "dns.google", Protocol: DOT},
		{Name: "Cloudflare 1 (DoT)", Address: "1.1.1.1", Protocol: DOT},
		{Name: "Cloudflare 2 (DoT)", Address: "one.one.one.one", Protocol: DOT},

		{Name: "AliDNS 1 (DoH)", Address: "https://dns.alidns.com/dns-query", Protocol: DOH},
		{Name: "AliDNS 2 (DoH)", Address: "https://223.5.5.5/dns-query", Protocol: DOH},
		{Name: "AliDNS 3 (DoH)", Address: "https://223.6.6.6/dns-query", Protocol: DOH},
		{Name: "DNSPod (DoH)", Address: "https://doh.pub/dns-query", Protocol: DOH},
		{Name: "Cloudflare 1 (DoH)", Address: "https://cloudflare-dns.com/dns-query", Protocol: DOH},
		{Name: "Cloudflare 2 (DoH)", Address: "https://1.1.1.1/dns-query", Protocol: DOH},
		{Name: "Cloudflare 3 (DoH)", Address: "https://1.0.0.1/dns-query", Protocol: DOH},
		{Name: "Google (DoH)", Address: "https://dns.google/resolve", Protocol: DOH},
	}

	defaultDomains = []string{
		"douyin.com", "kuaishou.com", "baidu.com", "taobao.com", "mi.com", "aliyun.com",
		"bilibili.com", "jd.com", "qq.com", "ithome.com", "hupu.com", "feishu.cn",
		"sohu.com", "163.com", "sina.com", "weibo.com", "xiaohongshu.com",
		"douban.com", "zhihu.com", "youku.com", "youdao.com", "mp.weixin.qq.com",
		"iqiyi.com", "v.qq.com", "y.qq.com", "www.ctrip.com", "autohome.com.cn",
		"google.com", "facebook.com", "x.com", "github.com", "youtube.com", "chatgpt.com",
		"apple.com", "bing.com", "tiktok.com",
	}
)

var rootCmd = &cobra.Command{
	Use:   "dns-optimizer",
	Short: "一个跨平台的 DNS 选优工具",
	Long:  `通过对一组常用域名进行并发测试，为您的网络环境推荐最快、最稳定的DNS服务器。`,
	Run:   runBenchmark, // Cobra会调用这个函数来执行主逻辑
}

var (
	domainsStr       string
	queriesPerDomain int
	queryTimeout     time.Duration
	maxConcurrency   int
)

func init() {
	rootCmd.PersistentFlags().StringVarP(&domainsStr, "domains", "d", strings.Join(defaultDomains, ","), "用于测试的域名列表, 以逗号分隔")
	rootCmd.PersistentFlags().IntVarP(&queriesPerDomain, "queries", "q", 3, "每个域名的查询次数")
	rootCmd.PersistentFlags().DurationVarP(&queryTimeout, "timeout", "t", 2*time.Second, "单次查询超时时间")
	rootCmd.PersistentFlags().IntVarP(&maxConcurrency, "concurrency", "c", 16, "同时测试的服务器数量上限")
}

// parseDomains 拆分、去空格并去重域名列表，保持原始顺序。
func parseDomains(raw string) []string {
	seen := make(map[string]struct{})
	var domains []string
	for d := range strings.SplitSeq(raw, ",") {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}
		domains = append(domains, d)
	}
	return domains
}

// runBenchmark
func runBenchmark(cmd *cobra.Command, args []string) {
	testDomains := parseDomains(domainsStr)
	if len(testDomains) == 0 {
		fmt.Println("错误: 没有有效的测试域名。")
		os.Exit(1)
	}
	totalQueries := len(dnsServers) * len(testDomains) * queriesPerDomain

	fmt.Println("DNS 选优工具: 开始对", len(dnsServers), "个 DNS 服务器进行综合基准测试...")
	fmt.Printf("测试域名 (%d个): %s\n", len(testDomains), strings.Join(testDomains, ", "))
	fmt.Printf("每个域名查询 %d 次, 总计 %d 次查询。\n\n", queriesPerDomain, totalQueries)

	// --- 1. 初始化进度条 ---
	bar := progressbar.NewOptions(totalQueries,
		progressbar.OptionSetWriter(color.Output),
		progressbar.OptionEnableColorCodes(true),
		progressbar.OptionSetDescription("[cyan]Running queries[reset]"),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "[green]=[reset]",
			SaucerHead:    "[green]>[reset]",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}),
	)

	// --- 2. 并发执行测试 ---
	// 每个服务器一个 goroutine，内部顺序查询，从而复用连接（DoT/DoH）并
	// 避免成千上万个请求同时打出导致互相争抢、污染延迟测量。
	// 服务器级别的并发由 sem 限制。
	resultsChan := make(chan QueryResult, totalQueries)
	sem := make(chan struct{}, max(1, maxConcurrency))
	var wg sync.WaitGroup

	for _, server := range dnsServers {
		wg.Add(1)
		go func(s DNSServer) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			benchmarkServer(s, testDomains, queriesPerDomain, resultsChan, bar)
		}(server)
	}

	wg.Wait()
	close(resultsChan)
	fmt.Println()

	// --- 3. 聚合结果时显示 Spinner 动画 ---
	s := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
	s.Suffix = " 正在聚合和计算评分..."
	s.Start()

	serverStats := aggregateResults(resultsChan)

	s.Stop()
	fmt.Println()

	// --- 4. 计算最终结果和评分 ---
	benchmarkResults := calculateScores(serverStats)

	// --- 5. 使用 tablewriter 打印结果 ---
	fmt.Println("--- 综合测试结果 ---")
	printResultsTable(benchmarkResults)

	fmt.Println("\n--- 最佳DNS推荐 (Top 3) ---")
	printRecommendations(benchmarkResults)
}

// benchmarkServer 对单个服务器顺序执行所有查询。
// 先做一次不计入结果的预热查询，让 DoT/DoH 建立连接、缓存服务器域名解析，
// 使各协议的测量进入稳定状态、更具可比性。
func benchmarkServer(server DNSServer, domains []string, queries int, ch chan<- QueryResult, bar *progressbar.ProgressBar) {
	q, closeFn := newQuerier(server)
	defer closeFn()

	// 预热（结果丢弃）。
	_, _ = q(domains[0])

	for _, domain := range domains {
		for range queries {
			d, err := q(domain)
			ch <- QueryResult{Server: server, Domain: domain, Duration: d, Err: err}
			bar.Add(1)
		}
	}
}

// dohResponse 是 application/dns-json 响应的最小子集（RFC 8484 风格）。
type dohResponse struct {
	Status int `json:"Status"`
}

// querier 执行一次对某域名的查询并返回耗时。
type querier func(domain string) (time.Duration, error)

// newQuerier 为某个服务器构造一个可复用的查询函数及其清理函数。
// 服务器主机名在此处预先解析为 IP，避免把系统 DNS 的解析耗时计入测量。
func newQuerier(server DNSServer) (querier, func()) {
	switch server.Protocol {
	case UDP:
		ip := resolveHost(server.Address)
		client := &dns.Client{Net: "udp", Timeout: queryTimeout}
		return reusableExchange(client, net.JoinHostPort(ip, "53"))

	case DOT:
		ip := resolveHost(server.Address)
		client := &dns.Client{
			Net:       "tcp-tls",
			Timeout:   queryTimeout,
			TLSConfig: &tls.Config{ServerName: server.Address},
		}
		return reusableExchange(client, net.JoinHostPort(ip, "853"))

	case DOH:
		client := &http.Client{Timeout: queryTimeout}
		q := func(domain string) (time.Duration, error) {
			start := time.Now()
			err := dohQuery(client, server.Address, domain)
			return time.Since(start), err
		}
		return q, client.CloseIdleConnections

	default:
		q := func(domain string) (time.Duration, error) {
			return 0, fmt.Errorf("不支持的协议: %s", server.Protocol)
		}
		return q, func() {}
	}
}

// reusableExchange 维护一条持久连接（UDP socket 或 DoT 的 TLS 连接），
// 在多次查询间复用，使各次测量只反映单次查询往返而非每次重新握手。
// 连接失效时会自动重连重试一次。同一 querier 仅在单个 goroutine 中顺序使用，无需加锁。
func reusableExchange(client *dns.Client, addr string) (querier, func()) {
	var conn *dns.Conn

	exchange := func(m *dns.Msg) (*dns.Msg, error) {
		if conn == nil {
			c, err := client.Dial(addr)
			if err != nil {
				return nil, err
			}
			conn = c
		}
		r, _, err := client.ExchangeWithConn(m, conn)
		return r, err
	}

	query := func(domain string) (time.Duration, error) {
		m := new(dns.Msg)
		m.SetQuestion(dns.Fqdn(domain), dns.TypeA)

		start := time.Now()
		r, err := exchange(m)
		if err != nil {
			// 连接可能已被对端关闭，丢弃后重连重试一次。
			if conn != nil {
				conn.Close()
				conn = nil
			}
			r, err = exchange(m)
		}
		elapsed := time.Since(start)

		if err != nil {
			if conn != nil {
				conn.Close()
				conn = nil
			}
			return elapsed, err
		}
		if r.Rcode != dns.RcodeSuccess {
			return elapsed, fmt.Errorf("DNS 响应码 %s", dns.RcodeToString[r.Rcode])
		}
		return elapsed, nil
	}

	closeFn := func() {
		if conn != nil {
			conn.Close()
			conn = nil
		}
	}
	return query, closeFn
}

// dohQuery 发起一次 DoH 查询并校验返回内容（而不仅仅是 HTTP 状态码）。
func dohQuery(client *http.Client, endpoint, domain string) error {
	reqURL := fmt.Sprintf("%s?name=%s&type=A", endpoint, domain)
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/dns-json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body) // 读完以便连接复用
		return fmt.Errorf("HTTP status %d", resp.StatusCode)
	}

	var parsed dohResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return fmt.Errorf("解析 DoH 响应失败: %w", err)
	}
	if parsed.Status != 0 {
		return fmt.Errorf("DoH 响应状态码 %d", parsed.Status)
	}
	return nil
}

// resolveHost 把主机名解析为 IP；若本身已是 IP 或解析失败，则原样返回。
func resolveHost(host string) string {
	if net.ParseIP(host) != nil {
		return host
	}
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil || len(addrs) == 0 {
		return host
	}
	return addrs[0]
}

// aggregateResults 负责从 channel 收集并聚合数据
func aggregateResults(resultsChan <-chan QueryResult) map[string]*serverStat {
	serverStats := make(map[string]*serverStat)
	for result := range resultsChan {
		stats, ok := serverStats[result.Server.Name]
		if !ok {
			stats = &serverStat{address: result.Server.Address}
			serverStats[result.Server.Name] = stats
		}
		stats.total++
		if result.Err == nil {
			stats.totalTime += result.Duration
			stats.successes++
		}
	}
	return serverStats
}

// calculateScores 计算最终的 BenchmarkResult 列表
func calculateScores(serverStats map[string]*serverStat) []BenchmarkResult {
	var benchmarkResults []BenchmarkResult
	for name, stats := range serverStats {
		res := BenchmarkResult{
			Name: name, Address: stats.address, Successes: stats.successes, Total: stats.total,
		}

		if stats.successes > 0 {
			res.AvgTime = stats.totalTime / time.Duration(stats.successes)
			res.SuccessRate = float64(stats.successes) / float64(stats.total)
			latencyScore := 1.0 / res.AvgTime.Seconds()
			res.Score = latencyScore * (res.SuccessRate * res.SuccessRate)
		}
		benchmarkResults = append(benchmarkResults, res)
	}

	sort.Slice(benchmarkResults, func(i, j int) bool {
		return benchmarkResults[i].Score > benchmarkResults[j].Score
	})

	return benchmarkResults
}

// printResultsTable 使用 tablewriter 打印漂亮的表格
func printResultsTable(results []BenchmarkResult) {
	table := tablewriter.NewWriter(os.Stdout)
	table.Header([]string{"DNS服务器", "地址", "平均延迟", "成功率", "综合评分"})

	green := color.New(color.FgGreen).SprintFunc()
	red := color.New(color.FgRed).SprintFunc()

	for _, r := range results {
		rateStr := fmt.Sprintf("%.1f%% (%d/%d)", r.SuccessRate*100, r.Successes, r.Total)
		if r.SuccessRate < 1.0 {
			rateStr = red(rateStr)
		} else {
			rateStr = green(rateStr)
		}

		table.Append([]string{
			r.Name,
			r.Address,
			r.AvgTime.Round(time.Microsecond).String(),
			rateStr,
			fmt.Sprintf("%.2f", r.Score),
		})
	}
	table.Render()
}

// printRecommendations 打印推荐
func printRecommendations(results []BenchmarkResult) {
	palette := []*color.Color{
		color.New(color.FgGreen, color.Bold),
		color.New(color.FgYellow),
		color.New(color.FgCyan),
	}
	red := color.New(color.FgRed)

	found := 0
	for _, best := range results {
		if best.SuccessRate <= 0.98 {
			continue
		}
		palette[found].Printf("#%d: %s (%s)\n", found+1, best.Name, best.Address)
		fmt.Printf("    综合评分: %.2f, 平均延迟: %s, 成功率: %.1f%%\n",
			best.Score, best.AvgTime.Round(time.Microsecond).String(), best.SuccessRate*100)
		found++
		if found >= len(palette) {
			break
		}
	}
	if found == 0 {
		red.Println("没有找到表现足够好的DNS服务器，请检查网络连接。")
	}
}

// main
func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
