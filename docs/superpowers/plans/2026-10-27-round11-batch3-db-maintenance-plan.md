# Round 11 批3 DB 维护 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]` syntax for tracking.

**Goal:** SQLite 维护能力：`store.Stats`（页/WAL 统计）与 `store.Checkpoint`（TRUNCATE + optimize）；app 启动打统计、退出日志化关闭并 checkpoint。

**Architecture:** 新文件 `store/maintenance.go` 纯函数操作 `*sql.DB`（路径经 `PRAGMA database_list` 自取，:memory: 退化 0）；`app.Start` 在 Open 后打统计日志；`app.Stop` 不再吞 Shutdown/Close 错误，Shutdown 成功后 Checkpoint 再 Close，首个错误作为返回值。

**Tech Stack:** database/sql、modernc.org/sqlite（WAL 已在 Open 启用）。

---

### Task 1: store/maintenance.go Stats + Checkpoint（TDD）

**Files:**
- Create: `backend/internal/store/maintenance.go`
- Test: `backend/internal/store/maintenance_test.go`

- [ ] **Step 1: 写失败测试**

```go
package store

import (
	"path/filepath"
	"testing"
)

func TestStatsAndCheckpoint(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "maint.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.Exec("CREATE TABLE t(x TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO t VALUES ('a')"); err != nil {
		t.Fatal(err)
	}

	st, err := Stats(db)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if st["page_count"] <= 0 || st["page_size"] <= 0 {
		t.Fatalf("page stats wrong: %v", st)
	}
	if st["freelist_count"] < 0 || st["file_bytes"] <= 0 {
		t.Fatalf("file stats wrong: %v", st)
	}
	if st["wal_bytes"] <= 0 {
		t.Fatalf("wal should be non-empty after write: %v", st)
	}

	if err := Checkpoint(db); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	st2, err := Stats(db)
	if err != nil {
		t.Fatal(err)
	}
	if st2["wal_bytes"] != 0 {
		t.Fatalf("wal should be truncated to 0, got %d", st2["wal_bytes"])
	}
}

func TestStatsMemoryDB(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	st, err := Stats(db)
	if err != nil {
		t.Fatal(err)
	}
	if st["wal_bytes"] != 0 || st["file_bytes"] != 0 {
		t.Fatalf("memory db should have 0 file/wal bytes: %v", st)
	}
}
```

- [ ] **Step 2: RED**

```bash
cd backend && go test ./internal/store/ -run TestStats -count=1
```
Expected: FAIL（Stats/Checkpoint undefined，编译错）。

- [ ] **Step 3: 实现 maintenance.go**

```go
package store

import (
	"database/sql"
	"fmt"
	"os"
)

// Stats 返回 SQLite 文件级统计（页与 WAL 字节数），用于启动与诊断日志。
func Stats(db *sql.DB) (map[string]int64, error) {
	out := make(map[string]int64, 5)
	for _, p := range []string{"page_count", "page_size", "freelist_count"} {
		var v int64
		if err := db.QueryRow("PRAGMA " + p).Scan(&v); err != nil {
			return nil, fmt.Errorf("pragma %s: %w", p, err)
		}
		out[p] = v
	}
	path := dbFilePath(db)
	out["file_bytes"] = fileSize(path)
	if path == "" { // :memory: 等无文件 DB
		out["wal_bytes"] = 0
		return out, nil
	}
	out["wal_bytes"] = fileSize(path + "-wal")
	return out, nil
}

// Checkpoint 截断 WAL（TRUNCATE）并刷新查询规划器统计；用于优雅退出。
func Checkpoint(db *sql.DB) error {
	if _, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return fmt.Errorf("wal checkpoint: %w", err)
	}
	if _, err := db.Exec("PRAGMA optimize"); err != nil {
		return fmt.Errorf("optimize: %w", err)
	}
	return nil
}

func dbFilePath(db *sql.DB) string {
	rows, err := db.Query("PRAGMA database_list")
	if err != nil {
		return ""
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var seq int
		var name, file string
		if err := rows.Scan(&seq, &name, &file); err == nil && name == "main" {
			return file
		}
	}
	return ""
}

func fileSize(p string) int64 {
	fi, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return fi.Size()
}
```

- [ ] **Step 4: GREEN + 回归**

```bash
cd backend && go test ./internal/store/ -count=1 && go build ./... && go vet ./...
```
Expected: 全绿。

- [ ] **Step 5: Commit**

```bash
git add backend/internal/store/maintenance.go backend/internal/store/maintenance_test.go
git commit -m "feat(store): Stats and Checkpoint maintenance helpers"
```

### Task 2: app 接线 — 启动统计 + Stop 日志化关闭（TDD）

**Files:**
- Modify: `backend/internal/app/app.go`（Start 统计日志 + Stop 重写）
- Test: `backend/internal/app/app_test.go`（新建，internal 包测试）

- [ ] **Step 1: 写失败测试 app_test.go**

```go
package app

import (
	"context"
	"path/filepath"
	"testing"

	"rivus-agent-backend/internal/store"
)

func TestStopCheckpointsAndCloses(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "stop.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec("CREATE TABLE t(x)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO t VALUES ('a')"); err != nil {
		t.Fatal(err)
	}

	a := &App{log: tLogger(), db: db}
	if err := a.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}

	// WAL 应被截断为 0（TRUNCATE 语义）
	wal := stSize(dbPath + "-wal")
	if wal != 0 {
		t.Fatalf("wal not truncated: %d", wal)
	}
	// 已关闭的 db 再执行查询应报错（证明 Close 生效）
	if _, err := db.Query("SELECT 1"); err == nil {
		t.Fatal("db should be closed")
	}
}
```
（`tLogger()` = slog.New(slog.NewTextHandler(io.Discard, nil))；`stSize` = os.Stat helper — 测试文件内定义。）

- [ ] **Step 2: RED → 实现 app.go**

Start：`store.Open` 之后（repos 构建前）插入：

```go
if st, err := store.Stats(db); err == nil {
	a.log.Info("db stats",
		"page_count", st["page_count"], "page_size", st["page_size"],
		"freelist_count", st["freelist_count"], "file_bytes", st["file_bytes"],
		"wal_bytes", st["wal_bytes"])
} else {
	a.log.Warn("db stats unavailable", "err", err)
}
```

Stop 重写（不再吞错误，首个错误返回）：

```go
func (a *App) Stop(ctx context.Context) error {
	var firstErr error
	if a.manager != nil {
		if err := a.manager.StopAll(); err != nil {
			a.log.Warn("mcp stop failed", "err", err)
		}
	}
	if a.srv != nil {
		if err := a.srv.Shutdown(ctx); err != nil {
			a.log.Warn("http shutdown failed", "err", err)
			firstErr = err
		}
	}
	if a.db != nil {
		if err := store.Checkpoint(a.db); err != nil {
			a.log.Warn("db checkpoint failed", "err", err)
		}
		if err := a.db.Close(); err != nil {
			a.log.Warn("db close failed", "err", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}
```

- [ ] **Step 3: GREEN + lint + 回归**

```bash
cd backend && go test ./internal/app/ -count=1 && $(go env GOPATH)/bin/golangci-lint run ./... && go test ./... -count=1 -timeout 600s
```
Expected: 全绿，golangci 0 issues。

- [ ] **Step 4: Commit**

```bash
git add backend/internal/app/app.go backend/internal/app/app_test.go
git commit -m "feat(app): db stats on start, logged shutdown with wal checkpoint"
```

---

## Self-review notes

- Stats 的 WAL 尺寸依赖 journal_mode=WAL（Open 已设）；:memory: DB `PRAGMA database_list` file 为空串 → 0。
- Checkpoint 在连接池（8 conns）下 TRUNCATE 需要无活跃读事务；退出场景 manager/HTTP 已停，安全。
- app_test 用 internal 包测试可访问私有字段；App 其余字段 nil 安全（Stop 有 nil 守卫）。
- Stop 返回首个错误（shutdown 或 close），checkpoint 失败仅告警不阻断退出。
