// Package buildinfo 承载构建时经 -ldflags "-X" 注入的版本信息。
// 默认值为本地源码运行（未注入）时的兜底。
package buildinfo

var (
	// Version 版本号；发布构建经 -ldflags 注入 git tag，未注入时为当前源码版本。
	Version = "0.1.0"
	// Commit 构建 commit 短哈希。
	Commit = "unknown"
	// BuildDate UTC 构建时间（RFC3339）。
	BuildDate = "unknown"
)
