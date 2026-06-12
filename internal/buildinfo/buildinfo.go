// Package buildinfo 保存编译期通过 -ldflags -X 注入的版本信息。
package buildinfo

import "fmt"

// 这些变量在构建时由 -ldflags "-X .../buildinfo.Version=..." 覆盖；
// 直接 go run / go build（未注入）时保持以下默认值。
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// String 返回适合 `--version` 输出的一行描述。
func String() string {
	return fmt.Sprintf("dnspick %s (commit %s, built %s)", Version, Commit, Date)
}
