package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/palemoky/dnspick/internal/dnsbench"
)

var (
	domainsStr       string
	queriesPerDomain int
	queryTimeout     time.Duration
	maxConcurrency   int
	noSystemDNS      bool
)

var rootCmd = &cobra.Command{
	Use:   "dnspick",
	Short: "一个跨平台的 DNS 选优工具",
	Long:  `通过对一组常用域名进行并发测试，为您的网络环境推荐最快、最稳定的DNS服务器。`,
	Run:   runBenchmark,
}

func init() {
	flags := rootCmd.PersistentFlags()
	flags.StringVarP(&domainsStr, "domains", "d", "", "自定义测试域名列表, 以逗号分隔（默认使用内置国内/国外域名）")
	flags.IntVarP(&queriesPerDomain, "queries", "q", 3, "每个域名的查询次数")
	flags.DurationVarP(&queryTimeout, "timeout", "t", 2*time.Second, "单次查询超时时间")
	flags.IntVarP(&maxConcurrency, "concurrency", "c", 16, "同时测试的服务器数量上限")
	flags.BoolVar(&noSystemDNS, "no-system-dns", false, "不检测、不测试当前系统默认 DNS")
}

func runBenchmark(cmd *cobra.Command, args []string) {
	// 域名：用户传了 -d 用自定义（归入“自定义”分类），否则用内置分类列表。
	domains := dnsbench.DefaultDomains
	if cmd.Flags().Changed("domains") {
		domains = dnsbench.ParseDomains(domainsStr)
	}
	if len(domains) == 0 {
		fmt.Println("错误: 没有有效的测试域名。")
		os.Exit(1)
	}

	// 服务器：内置列表 + （未禁用时）系统当前默认 DNS。
	servers := dnsbench.DefaultServers
	if !noSystemDNS {
		if sys := dnsbench.DetectSystemDNS(); len(sys) > 0 {
			servers = append(append([]dnsbench.Server{}, servers...), sys...)
		}
	}

	fmt.Printf("DNS 选优工具: 开始对 %d 个 DNS 服务器、%d 个域名进行综合基准测试...\n\n",
		len(servers), len(domains))

	tracker := newStatusTracker(domains, len(servers), queriesPerDomain)
	tracker.Start()
	results := dnsbench.Run(dnsbench.Options{
		Servers:     servers,
		Domains:     domains,
		Queries:     queriesPerDomain,
		Timeout:     queryTimeout,
		Concurrency: maxConcurrency,
	}, tracker.Progress)
	tracker.Stop()

	fmt.Println("\n--- 综合测试结果 ---")
	printResultsTable(results)

	fmt.Println("\n--- 最佳DNS推荐 (Top 3) ---")
	printRecommendations(results)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
