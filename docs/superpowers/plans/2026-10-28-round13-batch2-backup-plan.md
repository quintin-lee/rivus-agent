# Round-13 批2: SQLite 备份与会话导出 — 实现计划

> **For agentic workers:** REQUIRED: Use superpowers:executing-plans to implement this plan（用户约束：不使用 subagent）。Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `store.Backup`（`VACUUM INTO` 快照）+ `agentd export -session X` 子命令（单会话 runs/events/memories 导出为 JSON）。

**Architecture:** 备份 = 一条 `VACUUM INTO ?`（一致性快照，零新依赖）；导出 = cmd/agentd 新子命令，复用 config.Load + store.Open + 现有 repo 只读方法，payload 组装拆成可测的 `buildExportPayload`，runExport 只做 flag/IO。不新增 HTTP 端点（避免新认证面）。

**Tech Stack:** Go 1.27 / modernc.org/sqlite v1.60.1（SQLite 3.27+ 支持 `VACUUM INTO`）/ 标准库 flag/json。

---

## 已核实事实（写代码前已读源码）

- `internal/store/maintenance.go`：已有 `Stats`/`Checkpoint`/`dbFilePath(db) string`（:memory: 返回 ""）/`fileSize`；imports 已含 database/sql, fmt, os。
- `internal/store/maintenance_test.go`：package store，直接 `Open(filepath.Join(t.TempDir(), "maint.db"))`，无共享 helper。
- `cmd/agentd/main.go`：35 行，flag 只有 `-config`；无子命令分发。拆分方案：`main()` 只做 os.Args[1]=="export" 判断，其余走 `runServe()`（原逻辑整体平移）。
- `config.Load(path string) (Config, error)`；`store.Open(path) (*sql.DB, error)`。
- `TaskRepo.GetSession(ctx, ownerID, sessionID) (string, string, int64, int64, error)`（owner 不匹配 → error）。
- `TaskRepo.ListRunsBySession(ctx, ownerID, sessionID, limit)`：**limit ≤0 或 >100 会被钳到 50** → 导出传 100；若返回恰好 100 条，向 stderr 提示可能截断、建议用 backup 获取完整数据（spec 偏差：原设计"big-limit"不可行，钳制保留）。
- `EventRepo.ListAfter(ctx, runID, after, limit)`：LIMIT 直接透传 → **limit=-1 = SQLite 无上限**，导出传 -1。
- `MemoryRepo.Latest(ctx, sessionID, limit)`：limit≤0→20，**无上限钳制** → 导出传 1<<20。
- `MemoryRepo.Write` 签名与 `domain.Memory` 字段以执行时实读为准（memory_repo.go:38 附近）。
- POST /api/v1/sessions 的请求体形状执行 Task 3 时实读 handlers_sessions.go。

---

### Task 1: store.Backup（TDD）

**Files:**
- Modify: `backend/internal/store/maintenance.go`
- Test: `backend/internal/store/maintenance_test.go`

- [ ] **Step 1: 写失败测试**（追加到 maintenance_test.go，imports 需补 `database/sql`）

```go
func TestBackupRoundtrip(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "src.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec("CREATE TABLE t(x TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO t VALUES ('a')"); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(dir, "backup.db")
	if err := Backup(db, dest); err != nil {
		t.Fatalf("backup: %v", err)
	}

	// 备份文件是完整可用的 rivus 库：用 store.Open 再次打开并查询。
	bdb, err := Open(dest)
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer func() { _ = bdb.Close() }()
	var n int
	if err := bdb.QueryRow("SELECT count(*) FROM t").Scan(&n); err != nil {
		t.Fatalf("query backup: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 row in backup, got %d", n)
	}
}

func TestBackupRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "src.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	dest := filepath.Join(dir, "exists.db")
	if err := os.WriteFile(dest, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Backup(db, dest); err == nil {
		t.Fatal("want error for existing destination")
	}
}

func TestBackupRejectsMemoryDB(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := Backup(db, filepath.Join(t.TempDir(), "b.db")); err == nil {
		t.Fatal("want error for :memory: source")
	}
}
```

- [ ] **Step 2: 运行确认失败** — `cd backend && go test ./internal/store/ -run TestBackup -count=1` → 编译错误 `undefined: Backup`。
- [ ] **Step 3: 实现**（追加到 maintenance.go，Check-point 之后；imports 已含 os/fmt/sql 无需改动）

```go
// Backup 用 VACUUM INTO 生成一致性快照到 destPath。目标存在时报错（不覆盖）；
// :memory: 源不支持（无文件可写）。
func Backup(db *sql.DB, destPath string) error {
	if dbFilePath(db) == "" {
		return fmt.Errorf("backup unsupported for in-memory database")
	}
	if _, err := os.Stat(destPath); err == nil {
		return fmt.Errorf("backup destination already exists: %s", destPath)
	}
	if _, err := db.Exec("VACUUM INTO ?", destPath); err != nil {
		return fmt.Errorf("vacuum into: %w", err)
	}
	return nil
}
```

（若 modernc 驱动不支持 `VACUUM INTO ?` 参数绑定：改用校验路径只含 `[A-Za-z0-9._/-]` 后 `fmt.Sprintf("VACUUM INTO '%s'", destPath)`，并在计划偏差中记录。）

- [ ] **Step 4: 运行确认通过** — 同 Step 2 命令 → 3 个测试 PASS；`go vet ./internal/store/` 干净。
- [ ] **Step 5: Commit**

```bash
git add backend/internal/store/maintenance.go backend/internal/store/maintenance_test.go
git commit -m "feat(store): Backup via VACUUM INTO with destination guard"
```

---

### Task 2: agentd export 子命令（TDD）

**Files:**
- Create: `backend/cmd/agentd/export.go`
- Create: `backend/cmd/agentd/export_test.go`
- Modify: `backend/cmd/agentd/main.go`（拆 `runServe` + 分发）

- [ ] **Step 1: 写失败测试**（package main；实读 memory_repo.go 确认 Write 签名后落码）

```go
package main

import (
	"context"
	"path/filepath"
	"testing"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/store"
)

func newExportDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "exp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	tasks := store.NewTaskRepo(db)
	sid, err := tasks.CreateSession(context.Background(), "u1", "exp-session")
	if err != nil {
		t.Fatal(err)
	}
	run := &domain.Run{ID: "run_1", SessionID: sid, OwnerID: "u1", Goal: "g1",
		Mode: "react", Status: domain.RunSucceeded, Budget: domain.Budget{MaxModelCalls: 10}}
	if err := tasks.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	events := store.NewEventRepo(db)
	if _, err := events.Append(context.Background(), "run_1", domain.EvtRunStarted, `{"x":1}`, ""); err != nil {
		t.Fatal(err)
	}
	// memory 写入按 memory_repo.go 实际签名调整
	if err := store.NewMemoryRepo(db).Write(context.Background(), &domain.Memory{
		SessionID: sid, OwnerID: "u1", Content: "m1", SourceRunID: "run_1"}); err != nil {
		t.Fatal(err)
	}
	return db, sid
}

func TestBuildExportPayload(t *testing.T) {
	db, sid := newExportDB(t)
	p, err := buildExportPayload(db, sid)
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	if p.SessionID != sid || len(p.Runs) != 1 || len(p.Events) != 1 || len(p.Memories) != 1 {
		t.Fatalf("payload incomplete: %+v", p)
	}
	if p.Runs[0].Goal != "g1" || p.Events[0].RunID != "run_1" || p.Memories[0].Content != "m1" {
		t.Fatalf("payload fields wrong: %+v", p)
	}
}

func TestBuildExportPayloadMissingSession(t *testing.T) {
	db, _ := newExportDB(t)
	if _, err := buildExportPayload(db, "ses_missing"); err == nil {
		t.Fatal("want error for missing session")
	}
}
```

（imports 需 `database/sql`。若 `CreateRun`/`Memory.Write` 签名或必填列与假设不符，按实读修正 helper —— 断言保持不变。）

- [ ] **Step 2: 运行确认失败** — `cd backend && go test ./cmd/agentd/ -count=1` → `undefined: buildExportPayload`。
- [ ] **Step 3: 实现 export.go + main.go 拆分**

export.go：

```go
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"rivus-agent-backend/internal/config"
	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/store"
)

const exportRunLimit = 100 // ListRunsBySession 钳制上限

type exportPayload struct {
	SessionID string              `json:"session_id"`
	Runs      []domain.Run        `json:"runs"`
	Events    []domain.AgentEvent `json:"events"`
	Memories  []domain.Memory     `json:"memories"`
}

// buildExportPayload 汇总单会话的 runs / events / memories（只读）。
func buildExportPayload(db *sql.DB, sessionID string) (exportPayload, error) {
	ctx := context.Background()
	tasks := store.NewTaskRepo(db)

	var owner string
	err := db.QueryRowContext(ctx, "SELECT owner_id FROM sessions WHERE id = ?", sessionID).Scan(&owner)
	if err == sql.ErrNoRows {
		return exportPayload{}, fmt.Errorf("session %q not found", sessionID)
	} else if err != nil {
		return exportPayload{}, fmt.Errorf("find session: %w", err)
	}
	if _, _, _, _, err := tasks.GetSession(ctx, owner, sessionID); err != nil {
		return exportPayload{}, fmt.Errorf("load session: %w", err)
	}

	runs, err := tasks.ListRunsBySession(ctx, owner, sessionID, exportRunLimit)
	if err != nil {
		return exportPayload{}, fmt.Errorf("list runs: %w", err)
	}

	events := []domain.AgentEvent{}
	eventRepo := store.NewEventRepo(db)
	for _, r := range runs {
		evs, err := eventRepo.ListAfter(ctx, r.ID, 0, -1) // -1 = 无上限
		if err != nil {
			return exportPayload{}, fmt.Errorf("list events of run %s: %w", r.ID, err)
		}
		events = append(events, evs...)
	}

	memories, err := store.NewMemoryRepo(db).Latest(ctx, sessionID, 1<<20)
	if err != nil {
		return exportPayload{}, fmt.Errorf("list memories: %w", err)
	}

	return exportPayload{SessionID: sessionID, Runs: runs, Events: events, Memories: memories}, nil
}

// runExport 处理 `agentd export` 子命令：flags + 输出，不启动服务。
func runExport(args []string) {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	cfgPath := fs.String("config", os.Getenv("AGENT_CONFIG"), "config file path (optional)")
	sessionID := fs.String("session", "", "session id to export (required)")
	out := fs.String("o", "", "output file (default stdout)")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if *sessionID == "" {
		fmt.Fprintln(os.Stderr, "export: -session is required")
		os.Exit(2)
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "export: load config: %v\n", err)
		os.Exit(1)
	}
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "export: open db: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	p, err := buildExportPayload(db, *sessionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "export: %v\n", err)
		os.Exit(1)
	}
	if len(p.Runs) == exportRunLimit {
		fmt.Fprintln(os.Stderr, "export: warning: run count hit the limit; use a full backup for complete data")
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "export: marshal: %v\n", err)
		os.Exit(1)
	}
	if *out == "" {
		_, _ = os.Stdout.Write(data)
		_, _ = fmt.Fprintln(os.Stdout)
		return
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "export: write file: %v\n", err)
		os.Exit(1)
	}
}
```

main.go 全文改为：

```go
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"rivus-agent-backend/internal/app"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "export" {
		runExport(os.Args[2:])
		return
	}
	runServe()
}

func runServe() {
	cfgPath := flag.String("config", os.Getenv("AGENT_CONFIG"), "config file path (optional)")
	flag.Parse()
	a, err := app.New(*cfgPath)
	if err != nil {
		slog.Error("init failed", "err", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = a.Stop(shut)
	}()
	if err := a.Start(ctx); err != nil {
		slog.Error("server exited", "err", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 4: 运行确认通过** — `go test ./cmd/agentd/ -count=1` PASS；`go build ./... && go vet ./...` 干净。
- [ ] **Step 5: Commit**

```bash
git add backend/cmd/agentd/export.go backend/cmd/agentd/export_test.go backend/cmd/agentd/main.go
git commit -m "feat(cli): agentd export subcommand exporting one session as JSON"
```

---

### Task 3: 真机冒烟 + 文档 + 全量验收

- [ ] **Step 1: 真实二进制冒烟**

```bash
cd backend && go build -o bin/agentd ./cmd/agentd
AGENT_ADDR=:18098 AGENT_DB_PATH=/tmp/rivus-export-smoke.db MODEL_BASE_URL=http://127.0.0.1:9 \
  MODEL_API_KEY=dummy MODEL_NAME=dummy ./bin/agentd & PID=$!
sleep 1.5
# 创建会话（先实读 handlers_sessions.go 的 createSessionReq 形状，再 curl）
curl -s -X POST localhost:18098/api/v1/sessions -H 'Content-Type: application/json' -d '<shape>'
kill $PID; sleep 0.5
./bin/agentd export -session <id> -o /tmp/rivus-export.json
python3 -c "import json;d=json.load(open('/tmp/rivus-export.json'));print(d['session_id'],len(d['runs']),len(d['events']),len(d['memories']))"
rm -f /tmp/rivus-export-smoke.db /tmp/rivus-export-smoke.db-wal /tmp/rivus-export-smoke.db-shm /tmp/rivus-export.json bin/agentd
```

- [ ] **Step 2: 文档 zh/en**（backend/README.md 与 README.en.md，紧跟「质量与维护」节后新增「备份与导出 / Backup & Export」小节）：
  - `store.Backup`（VACUUM INTO 一致性快照、目标存在报错、:memory: 不支持）+ `agentd export -session <id> [-o file]`（runs/events/memories JSON；runs 上限 100 提示用 backup）。
- [ ] **Step 3: 全量验收** — `go build ./... && go vet ./... && go test ./... -count=1 -timeout 600s`；`golangci-lint run`（若可用）0 issues；前端零漂移留到批3 统一验收。
- [ ] **Step 4: Commit**

```bash
git add backend/README.md backend/README.en.md
git commit -m "docs(ops): backup and export notes (zh/en)"
```

---

## Self-review 备注

1. `ListRunsBySession` 钳制（>100→50）使 spec 的「big-limit」不可行 → 导出传 100 + len==100 时 stderr 警告（已写入 spec 偏差）。
2. `Events.ListAfter` limit=-1 在 SQLite 中 = 无上限，直接透传 LIMIT，无需改 repo。
3. `VACUUM INTO ?` 参数绑定若失败按 Step 3 注释降级，记录偏差。
4. `runExport` 不做 `http.ErrAbortHandler` 类复杂清理；任何失败走 stderr + 非零退出，符合 ops CLI 语义。
5. 前端零改动；`git status` 应只有 docs/store/cmd 文件。
