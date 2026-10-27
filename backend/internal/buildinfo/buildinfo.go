// Package buildinfo 承载构建时经 -ldflags "-X" 注入的版本信息。
// 默认值为本地源码运行（未注入）时的兜底。
package buildinfo

var (
	// Version 版本号；发布构建注入 git tag（如 v1.2.3）。
	Version = "dev"
	// Commit 构建 commit 短哈希。
	Commit = "unknown"
	// BuildDate UTC 构建时间（RFC3339）。
	BuildDate = "unknown"
)
