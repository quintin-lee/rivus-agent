# Round-16 批1：agentd backup CLI + CHANGELOG 实施计划

> **For agentic workers:** REQUIRED: Use superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 R13 的 `store.Backup` 暴露为 `agentd backup -o <path>` 子命令，并建立仓库级 CHANGELOG。

**Architecture:** 复用 export 子命令模式（main.go os.Args 分发 + flag.NewFlagSet + config.Load/store.Open + stderr 摘要/exit 码）；CHANGELOG 为 Keep-a-Changelog 静态文档。

**Tech Stack:** Go 1.27 flag / modernc sqlite（VACUUM INTO）。

**Spec:** docs/superpowers/specs/2026-10-28-round16-cli-errorboundary-evals-design.md §3.1

---

## Chunk 1

### Task 1: backup 子命令（TDD）

**Files:**
- Modify: `backend/cmd/agentd/main.go`（分发 + runBackup 调用）
- Create: `backend/cmd/agentd/backup.go`
- Test: `backend/cmd/agentd/backup_test.go`

- [ ] **Step 1: 失败测试**——`TestRunBackupCreatesQueryableSnapshot`：newExportDB 风格夹具（store.Open t.TempDir + CreateSession + CreateRun）→ `runBackupForTest([]string{"-o", dest})` → err==nil → `store.Open(dest)` 重新打开 → `tasks.GetSession(ctx, owner, ses)` 成功。`TestRunBackupRequiresOutput`：无 `-o` → 返回 flag.ErrHelp 或显式错误（设计：runBackup 返回 error 供测试；os.Exit 只在 runExport 包装层）。
- [ ] **Step 2**: `go test ./cmd/agentd/ -run TestRunBackup -count=1` → RED（undefined runBackup）。
- [ ] **Step 3: 实现**——`backup.go`：

```go
func runBackup(args []string) // 真入口：解析 + os.Exit
func backupCore(args []string) error // 可测核心：解析 flag、config.Load、store.Open、store.Backup
```

main.go：`os.Args[1] == "backup"` → `runBackup(os.Args[2:])`。错误 stderr + exit 1；`-o` 缺失 stderr 用法 + exit 2；成功 stderr `已备份到 %s`。
- [ ] **Step 4**: `go test ./cmd/agentd/ -count=1` GREEN + `go build ./... && go vet ./...`。
- [ ] **Step 5: Commit** `feat(cli): agentd backup subcommand via store.Backup`

### Task 2: 真机冒烟 + CHANGELOG.md

- [ ] **Step 1**: `make build` → 临时 DB 起服务（同 R13 冒烟法：AGENT_ADDR=:18099 AGENT_DB_PATH=/tmp/r16-smoke.db）→ 建会话 → kill → `./bin/agentd backup -o /tmp/r16-backup.db`（带 AGENT_DB_PATH env）→ `store` 层验证：重新打开 /tmp/r16-backup.db 查 sessions（用小 Go 程序或 sqlite3 CLI 不可用则 go run 一段临时代码，用完删）→ 清理 bin 与临时文件。
- [ ] **Step 2**: 根目录 `CHANGELOG.md`——Keep a Changelog 头 + `## [Unreleased]`；Added（内置工具 7、tasks 并行、会话记忆、evals 30、观测三件套、限流/安全头、/version、MCP HTTP+热载、备份/导出 CLI、i18n/主题/运行历史/详情弹窗、ErrorBoundary）与 Fixed（幂等缓存、emit 丢事件、审批重试语义、http_fetch 端口匹配）按主题 ~10 行。
- [ ] **Step 3: Commit** `docs(changelog): initialize with round 1-16 summary`

## Self-review 备注

- runBackup 拆 `backupCore(args) error` + 薄 os.Exit 壳，测试不打 exit 路径（同 export.go 已有先例——核实后沿用其真实拆法）。
- store.Backup 拒绝覆盖已存在文件 → 冒烟前确保目标不存在。
- CHANGELOG 放仓库根目录（README 平级）。
