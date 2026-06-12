package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/olekukonko/tablewriter"

	"github.com/palemoky/dnspick/internal/dnsbench"
)

// printResultsTable 使用 tablewriter 打印漂亮的结果表格。
func printResultsTable(results []dnsbench.Result) {
	table := tablewriter.NewWriter(os.Stdout)
	table.Header([]string{"#", "DNS服务器", "地址", "平均延迟", "成功率", "综合评分"})

	green := color.New(color.FgGreen).SprintFunc()
	red := color.New(color.FgRed).SprintFunc()

	for i, r := range results {
		rateStr := fmt.Sprintf("%.1f%% (%d/%d)", r.SuccessRate*100, r.Successes, r.Total)
		if r.SuccessRate < 1.0 {
			rateStr = red(rateStr)
		} else {
			rateStr = green(rateStr)
		}

		name := r.Name
		if r.IsSystem {
			name += " (当前)"
		}
		table.Append([]string{
			fmt.Sprintf("%d", i+1),
			name,
			r.Address,
			r.AvgTime.Round(time.Microsecond).String(),
			rateStr,
			fmt.Sprintf("%.2f", r.Score),
		})
	}
	table.Render()
}

// printRecommendations 打印 Top 推荐。
func printRecommendations(results []dnsbench.Result) {
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

	if msg, ok := systemDNSVerdict(results); ok {
		c := color.New(color.FgGreen, color.Bold)
		if strings.HasPrefix(msg, "⚠") {
			c = color.New(color.FgYellow, color.Bold)
		}
		fmt.Println()
		c.Println(msg)
	}
}

const (
	// switchLatencyThreshold：系统 DNS 比最优慢不足此值时视为无意义差异，不建议切换。
	switchLatencyThreshold = 15 * time.Millisecond
	// switchSuccessMargin：系统 DNS 成功率落后最优超过此值时，即便延迟接近也建议切换。
	switchSuccessMargin = 0.05
)

// systemDNSVerdict 针对系统当前默认 DNS 给出是否需要调整的结论。
// 仅当系统 DNS 明显更慢（延迟差 ≥ switchLatencyThreshold）或可靠性明显更差时才建议切换，
// 避免因几毫秒的无意义差异误导用户。若结果中不含系统 DNS，则 ok 为 false。
// results 须按评分降序排列。
func systemDNSVerdict(results []dnsbench.Result) (msg string, ok bool) {
	if len(results) == 0 {
		return "", false
	}
	sysRank := -1
	for i := range results {
		if results[i].IsSystem {
			sysRank = i
			break
		}
	}
	if sysRank < 0 {
		return "", false
	}

	sys := results[sysRank]
	best := results[0]
	latencyGap := sys.AvgTime - best.AvgTime
	closeEnough := latencyGap < switchLatencyThreshold && best.SuccessRate-sys.SuccessRate <= switchSuccessMargin

	switch {
	case sys.Successes == 0:
		return fmt.Sprintf("⚠️  当前默认 DNS (%s) 查询全部失败，建议切换到 #1 %s (%s)。",
			sys.Address, best.Name, best.Address), true
	case sysRank == 0:
		return fmt.Sprintf("✅ 当前默认 DNS (%s) 已是最优，无需调整。", sys.Address), true
	case closeEnough:
		return fmt.Sprintf("✅ 当前默认 DNS (%s) 已足够好（排名第 %d，仅慢 %s），无需调整。",
			sys.Address, sysRank+1, latencyGap.Round(time.Microsecond)), true
	default:
		return fmt.Sprintf("⚠️  当前默认 DNS (%s) 排名第 %d，建议切换到 #1 %s (%s)：平均延迟 %s → %s。",
			sys.Address, sysRank+1, best.Name, best.Address,
			sys.AvgTime.Round(time.Microsecond), best.AvgTime.Round(time.Microsecond)), true
	}
}
