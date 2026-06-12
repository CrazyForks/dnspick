package main

import (
	"strings"
	"testing"
	"time"

	"github.com/palemoky/dnspick/internal/dnsbench"
)

func TestSystemDNSVerdict(t *testing.T) {
	// mk 按平均延迟(ms)与成功率构造结果；Successes 据成功率推导。
	mk := func(name string, avgMs int, rate float64, sys bool) dnsbench.Result {
		return dnsbench.Result{
			Name: name, Address: name,
			AvgTime:     time.Duration(avgMs) * time.Millisecond,
			SuccessRate: rate, Successes: int(rate * 10), Total: 10, IsSystem: sys,
		}
	}

	t.Run("no system dns", func(t *testing.T) {
		res := []dnsbench.Result{mk("A", 5, 1.0, false)}
		if _, ok := systemDNSVerdict(res); ok {
			t.Fatal("expected ok=false when no system DNS")
		}
	})

	t.Run("system is best", func(t *testing.T) {
		res := []dnsbench.Result{mk("sys", 5, 1.0, true), mk("B", 10, 1.0, false)}
		if msg, ok := systemDNSVerdict(res); !ok || !strings.Contains(msg, "已是最优") {
			t.Fatalf("got ok=%v msg=%q", ok, msg)
		}
	})

	// 用户场景：仅慢 2ms，不应建议切换。
	t.Run("system 2ms slower -> no switch", func(t *testing.T) {
		res := []dnsbench.Result{mk("A", 5, 1.0, false), mk("sys", 7, 1.0, true)}
		msg, ok := systemDNSVerdict(res)
		if !ok || strings.Contains(msg, "建议切换") {
			t.Fatalf("2ms gap should NOT recommend switching; got ok=%v msg=%q", ok, msg)
		}
		if !strings.Contains(msg, "无需调整") {
			t.Fatalf("expected 无需调整, got %q", msg)
		}
	})

	t.Run("system much slower -> switch", func(t *testing.T) {
		res := []dnsbench.Result{mk("A", 5, 1.0, false), mk("sys", 25, 1.0, true)}
		if msg, ok := systemDNSVerdict(res); !ok || !strings.Contains(msg, "建议切换") {
			t.Fatalf("got ok=%v msg=%q", ok, msg)
		}
	})

	// 延迟接近但可靠性明显更差，也应建议切换。
	t.Run("system flaky -> switch", func(t *testing.T) {
		res := []dnsbench.Result{mk("A", 5, 1.0, false), mk("sys", 6, 0.7, true)}
		if msg, ok := systemDNSVerdict(res); !ok || !strings.Contains(msg, "建议切换") {
			t.Fatalf("got ok=%v msg=%q", ok, msg)
		}
	})

	t.Run("system all failed -> switch", func(t *testing.T) {
		res := []dnsbench.Result{mk("A", 5, 1.0, false), mk("sys", 0, 0, true)}
		if msg, ok := systemDNSVerdict(res); !ok || !strings.Contains(msg, "全部失败") {
			t.Fatalf("got ok=%v msg=%q", ok, msg)
		}
	})
}
