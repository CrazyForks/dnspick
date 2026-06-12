package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/palemoky/dnspick/internal/buildinfo"
	"github.com/palemoky/dnspick/internal/dnsbench"
	"github.com/palemoky/dnspick/internal/ui"
	"github.com/palemoky/dnspick/internal/updater"
)

var (
	domainsStr       string
	queriesPerDomain int
	queryTimeout     time.Duration
	maxConcurrency   int
	noSystemDNS      bool
)

var rootCmd = &cobra.Command{
	Use:     "dnspick",
	Short:   "一个跨平台的 DNS 选优工具",
	Long:    `通过对一组常用域名进行并发测试，为您的网络环境推荐最快、最稳定的DNS服务器。`,
	Version: buildinfo.Version,
	Run:     runBenchmark,
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "显示版本信息",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(buildinfo.String())
	},
}

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "检查并更新到最新版本",
	Run:   runUpdate,
}

func init() {
	rootCmd.SetVersionTemplate("{{.Version}}\n")

	flags := rootCmd.PersistentFlags()
	flags.StringVarP(&domainsStr, "domains", "d", "", "自定义测试域名列表, 以逗号分隔（默认使用内置国内/国外域名）")
	flags.IntVarP(&queriesPerDomain, "queries", "q", 3, "每个域名的查询次数")
	flags.DurationVarP(&queryTimeout, "timeout", "t", 2*time.Second, "单次查询超时时间")
	flags.IntVarP(&maxConcurrency, "concurrency", "c", 16, "同时测试的服务器数量上限")
	flags.BoolVar(&noSystemDNS, "no-system-dns", false, "不检测、不测试当前系统默认 DNS")

	rootCmd.AddCommand(versionCmd, updateCmd)
}

func runUpdate(cmd *cobra.Command, args []string) {
	ctx, cancel := context.WithTimeout(context.Background(), updater.DefaultTimeout)
	defer cancel()

	fmt.Printf("当前版本: %s，正在检查更新...\n", buildinfo.Version)
	latest, updated, err := updater.Update(ctx, buildinfo.Version)
	if err != nil {
		fmt.Println("更新失败:", err)
		os.Exit(1)
	}
	if !updated {
		fmt.Printf("已是最新版本 (%s)。\n", latest)
		return
	}
	fmt.Printf("✓ 已更新到 %s。\n", latest)
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

	tracker := ui.NewStatusTracker(domains, len(servers), queriesPerDomain)
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
	ui.PrintResultsTable(results)

	fmt.Println("\n--- 最佳DNS推荐 (Top 3) ---")
	ui.PrintRecommendations(results)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
