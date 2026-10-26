# 批4：跨 Run 会话记忆 实现计划

> **For agentic workers:** 本计划由 executing-plans 技能执行（用户约束：不使用 subagent）。步骤用 checkbox 跟踪。

**Goal:** 会话级持久记忆：`session_memories` 表（migrate v3）+ `memory_write`/`memory_read` 工具 + 顶层 run 指令自动注入最近 10 条记忆 + `GET /api/v1/sessions/{id}/memories` 查询端点。

**Architecture:** MemoryRepo（store 层，LRU 100 条/会话，FK 级联删除）→ builtin.MemoryTools(repo) 两个工具（经 TaskContextFrom 取 session/owner）→ runtime.run() 取记忆拼进 RunRequest.MemoryContext → buildAgentFiltered 追加到 instruction（仅顶层，子 agent 不注入）。API 层复用 Server 已有 db 参数构造 MemoryRepo（签名不变）。

**Tech Stack:** Go 1.27 stdlib + modernc.org/sqlite（WAL，外键级联）；测试走既有 testutil harness。

**授权设计：** spec 2026-10-26-agent-capabilities-design.md §批4（decision A：memory_write 零审批）。

**YAGNI：** 自动摘要 / 向量检索 / 跨会话记忆 / 新事件类型 / 记忆编辑 API / 子 agent 注入。

---

## Chunk 1: 存储与工具

### Task 1: schema v3 + MemoryRepo

**Files:**
- Modify: `backend/internal/store/schema.sql`（末尾追加表）
- Modify: `backend/internal/store/sqlite.go`（migrate 加 version 3）
- Create: `backend/internal/domain/memory.go`
- Create: `backend/internal/store/memory_repo.go`
- Test: `backend/internal/store/memory_repo_test.go`

- [ ] **Step 1: 写失败测试** `backend/internal/store/memory_repo_test.go`（包 store，同包测试风格参考 settings_test.go）：

```go
package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"rivus-agent-backend/internal/domain"
)

func newMemDB(t *testing.T) (*sql.DB, *MemoryRepo) {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "mem.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, NewMemoryRepo(db)
}

func TestMemoryWriteAndLatest(t *testing.T) {
	_, repo := newMemDB(t)
	ctx := context.Background()
	m := &domain.Memory{SessionID: "ses_1", OwnerID: "o1", Content: "用户偏好简洁回复", SourceRunID: "run_1"}
	if err := repo.Write(ctx, m); err != nil {
		t.Fatal(err)
	}
	if m.ID == "" || m.CreatedAt == 0 {
		t.Fatal("Write must fill ID and CreatedAt")
	}
	got, err := repo.Latest(ctx, "ses_1", 10)
	if err != nil || len(got) != 1 || got[0].Content != "用户偏好简洁回复" {
		t.Fatalf("got=%v err=%v", got, err)
	}
	// 其他会话不可见
	other, _ := repo.Latest(ctx, "ses_other", 10)
	if len(other) != 0 {
		t.Fatal("session isolation broken")
	}
}

func TestMemoryLRUTrim(t *testing.T) {
	_, repo := newMemDB(t)
	ctx := context.Background()
	for i := 0; i < 105; i++ {
		_ = repo.Write(ctx, &domain.Memory{SessionID: "s", OwnerID: "o", Content: fmt.Sprintf("m%03d", i)})
	}
	got, err := repo.Latest(ctx, "s", 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 100 {
		t.Fatalf("want 100 after LRU trim, got %d", len(got))
	}
	// 最旧的 5 条应被淘汰：总数 105，剩下的是后写入的
	if got[0].Content != "m104" {
		t.Fatalf("newest first broken: %q", got[0].Content)
	}
	for _, m := range got {
		if m.Content == "m000" {
			t.Fatal("oldest memory should have been evicted")
		}
	}
}

func TestMemoryCascadeOnSessionDelete(t *testing.T) {
	db, repo := newMemDB(t)
	ctx := context.Background()
	tasks := NewTaskRepo(db)
	ses, err := tasks.CreateSession(ctx, "o1", "t")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Write(ctx, &domain.Memory{SessionID: ses, OwnerID: "o1", Content: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := tasks.DeleteSession(ctx, "o1", ses); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.Latest(ctx, ses, 10)
	if len(got) != 0 {
		t.Fatal("memories must cascade-delete with session")
	}
}
```

（Latest 的 ORDER BY 必须是 `created_at DESC, rowid DESC`：同毫秒写入靠 rowid 兜底排序。）

- [ ] **Step 2: 运行确认失败**

Run: `cd backend && go test ./internal/store/ -run TestMemory -v`
Expected: FAIL（MemoryRepo/domain.Memory 未定义，编译错误）

- [ ] **Step 3: 实现**

`backend/internal/domain/memory.go`：

```go
package domain

// Memory 是一条会话级持久记忆（跨 run 共享，随会话删除级联清除）。
type Memory struct {
	ID          string `json:"id"`
	SessionID   string `json:"session_id"`
	OwnerID     string `json:"owner_id"`
	Content     string `json:"content"`
	SourceRunID string `json:"source_run_id,omitempty"`
	CreatedAt   int64  `json:"created_at"`
}
```

`backend/internal/store/schema.sql` 末尾追加：

```sql
CREATE TABLE IF NOT EXISTS session_memories (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL,
  owner_id TEXT NOT NULL,
  content TEXT NOT NULL,
  source_run_id TEXT,
  created_at INTEGER NOT NULL,
  FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_memories_session ON session_memories(session_id, created_at);
```

`backend/internal/store/sqlite.go` migrate()：在 version 2 的 INSERT 之后加：

```go
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(3, ?)`, time.Now().UnixMilli()); err != nil {
		return err
	}
```

（建表走 schemaSQL 的 `CREATE TABLE IF NOT EXISTS`，天然幂等，无需 v2 那样的 colExists 守卫。）

`backend/internal/store/memory_repo.go`：

```go
package store

import (
	"context"
	"database/sql"

	"rivus-agent-backend/internal/domain"
)

// MemoryRepo 负责 session_memories 持久化（LRU 100 条/会话，会话删除级联）。
type MemoryRepo struct{ db *sql.DB }

func NewMemoryRepo(db *sql.DB) *MemoryRepo { return &MemoryRepo{db: db} }

const memoryMaxPerSession = 100

// Write 插入一条记忆并裁剪到最近 100 条（超出删最旧）。
func (r *MemoryRepo) Write(ctx context.Context, m *domain.Memory) error {
	if m.ID == "" {
		m.ID = newID("mem")
	}
	if m.CreatedAt == 0 {
		m.CreatedAt = nowMs()
	}
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO session_memories(id, session_id, owner_id, content, source_run_id, created_at) VALUES(?, ?, ?, ?, ?, ?)`,
		m.ID, m.SessionID, m.OwnerID, m.Content, m.SourceRunID, m.CreatedAt); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM session_memories WHERE session_id = ? AND rowid NOT IN
		 (SELECT rowid FROM session_memories WHERE session_id = ? ORDER BY created_at DESC, rowid DESC LIMIT ?)`,
		m.SessionID, m.SessionID, memoryMaxPerSession)
	return err
}

// Latest 返回某会话最近 limit 条记忆（新→旧）。
func (r *MemoryRepo) Latest(ctx context.Context, sessionID string, limit int) ([]domain.Memory, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, session_id, owner_id, content, source_run_id, created_at
		 FROM session_memories WHERE session_id = ? ORDER BY created_at DESC, rowid DESC LIMIT ?`,
		sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []domain.Memory
	for rows.Next() {
		var m domain.Memory
		var src sql.NullString
		if err := rows.Scan(&m.ID, &m.SessionID, &m.OwnerID, &m.Content, &src, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.SourceRunID = src.String
		out = append(out, m)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: 运行确认通过**（含既有 store 测试回归）

Run: `cd backend && go test ./internal/store/ ./internal/domain/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/store/schema.sql backend/internal/store/sqlite.go backend/internal/domain/memory.go backend/internal/store/memory_repo.go backend/internal/store/memory_repo_test.go
git commit -m "feat(store): session_memories table (migrate v3) with LRU trim and session cascade"
```

### Task 2: memory_write / memory_read 工具

**Files:**
- Create: `backend/internal/tool/builtin/memory.go`
- Test: `backend/internal/tool/builtin/memory_test.go`

- [ ] **Step 1: 写失败测试** `backend/internal/tool/builtin/memory_test.go`：

```go
package builtin

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"rivus-agent-backend/internal/runtime"
	"rivus-agent-backend/internal/store"
	"rivus-agent-backend/internal/tool"
)

func newMemTools(t *testing.T) []tool.Definition {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "mem.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return MemoryTools(store.NewMemoryRepo(db))
}

func callMem(t *testing.T, defs []tool.Definition, name, args string, tc *runtime.TaskContext) (string, error) {
	t.Helper()
	var def *tool.Definition
	for i := range defs {
		if defs[i].Name == name {
			def = &defs[i]
		}
	}
	if def == nil {
		t.Fatalf("tool %s not registered", name)
	}
	ctx := context.Background()
	if tc != nil {
		ctx = runtime.WithTaskContext(ctx, *tc)
	}
	return def.Handler(ctx, json.RawMessage(args))
}

func memTC() *runtime.TaskContext {
	return &runtime.TaskContext{OwnerID: "o1", SessionID: "ses_1", ParentRunID: "run_1", Depth: 0}
}

func TestMemoryWriteRoundtrip(t *testing.T) {
	defs := newMemTools(t)
	out, err := callMem(t, defs, "memory_write", `{"content":"用户偏好简洁回复"}`, memTC())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "saved") {
		t.Fatalf("unexpected output %q", out)
	}
	got, err := callMem(t, defs, "memory_read", `{}`, memTC())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "用户偏好简洁回复") {
		t.Fatalf("read back failed: %q", got)
	}
}

func TestMemoryWriteTooLarge(t *testing.T) {
	defs := newMemTools(t)
	big := strings.Repeat("字", 2049) // 2049 个中文字节 > 2048
	if _, err := callMem(t, defs, "memory_write", `{"content":"`+big+`"}`, memTC()); err == nil {
		t.Fatal("oversized content must be rejected")
	}
}

func TestMemoryWriteRequiresTaskContext(t *testing.T) {
	defs := newMemTools(t)
	if _, err := callMem(t, defs, "memory_write", `{"content":"x"}`, nil); err == nil {
		t.Fatal("missing task context must error")
	}
	if _, err := callMem(t, defs, "memory_read", `{}`, nil); err == nil {
		t.Fatal("missing task context must error")
	}
}

func TestMemoryReadQueryFilter(t *testing.T) {
	defs := newMemTools(t)
	_ = callMem(t, defs, "memory_write", `{"content":"喜欢 Go 语言"}`, memTC())
	_ = callMem(t, defs, "memory_write", `{"content":"讨厌冗长文档"}`, memTC())
	got, err := callMem(t, defs, "memory_read", `{"query":"GO"}`, memTC()) // 大小写不敏感
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "喜欢 Go 语言") || strings.Contains(got, "讨厌冗长文档") {
		t.Fatalf("query filter broken: %q", got)
	}
	// 无 query → 返回最近全部（≤20）
	all, err := callMem(t, defs, "memory_read", `{}`, memTC())
	if err != nil || !strings.Contains(all, "讨厌冗长文档") {
		t.Fatalf("no-query read broken: %q err=%v", all, err)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd backend && go test ./internal/tool/builtin/ -run TestMemory -v`
Expected: FAIL（MemoryTools 未定义）

- [ ] **Step 3: 实现** `backend/internal/tool/builtin/memory.go`：

```go
package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/runtime"
	"rivus-agent-backend/internal/store"
	"rivus-agent-backend/internal/tool"
)

const (
	memoryMaxBytes   = 2048 // 单条上限
	memoryReadLatest = 20   // 无 query 时返回条数
)

// MemoryTools 返回 memory_write / memory_read 两个工具定义。
// repo 由调用方注入（app.go / harness），builtin 保持无全局状态。
func MemoryTools(repo *store.MemoryRepo) []tool.Definition {
	return []tool.Definition{
		{
			Name:        "memory_write",
			Description: "把值得长期记住的事实写入会话记忆（跨 run 共享）。Args: {\"content\":\"<一句事实>\"}",
			Version:     "v1",
			InputSchema: `{"type":"object","properties":{"content":{"type":"string"}},"required":["content"]}`,
			Risk:        tool.RiskWrite, Approval: tool.ApprovalNever, Idempotent: false,
			TimeoutMs: 5000, MaxResultBytes: 1024,
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				var in struct{ Content string `json:"content"` }
				if err := json.Unmarshal(args, &in); err != nil || strings.TrimSpace(in.Content) == "" {
					return "", fmt.Errorf("memory_write: content required")
				}
				if len(in.Content) > memoryMaxBytes {
					return "", fmt.Errorf("memory_write: content too large (%d > %d bytes)", len(in.Content), memoryMaxBytes)
				}
				tc, ok := runtime.TaskContextFrom(ctx)
				if !ok {
					return "", fmt.Errorf("memory_write: no task context")
				}
				if err := repo.Write(ctx, &domain.Memory{
					SessionID: tc.SessionID, OwnerID: tc.OwnerID,
					Content: strings.TrimSpace(in.Content), SourceRunID: tc.ParentRunID,
				}); err != nil {
					return "", fmt.Errorf("memory_write: %w", err)
				}
				return `{"status":"saved"}`, nil
			},
		},
		{
			Name:        "memory_read",
			Description: "读取会话记忆；可带 query 子串过滤（大小写不敏感），不带则返回最近 20 条。Args: {\"query\":\"<可选>\"}",
			Version:     "v1",
			InputSchema: `{"type":"object","properties":{"query":{"type":"string"}}}`,
			Risk:        tool.RiskRead, Approval: tool.ApprovalNever, Idempotent: true,
			TimeoutMs: 5000, MaxResultBytes: 16 * 1024,
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				var in struct{ Query string `json:"query"` }
				_ = json.Unmarshal(args, &in)
				tc, ok := runtime.TaskContextFrom(ctx)
				if !ok {
					return "", fmt.Errorf("memory_read: no task context")
				}
				mems, err := repo.Latest(ctx, tc.SessionID, 200)
				if err != nil {
					return "", fmt.Errorf("memory_read: %w", err)
				}
				q := strings.ToLower(strings.TrimSpace(in.Query))
				lines := make([]string, 0, len(mems))
				for _, m := range mems {
					if q != "" && !strings.Contains(strings.ToLower(m.Content), q) {
						continue
					}
					lines = append(lines, "- "+m.Content)
					if len(lines) >= memoryReadLatest {
						break
					}
				}
				if len(lines) == 0 {
					return "（无记忆）", nil
				}
				return strings.Join(lines, "\n"), nil
			},
		},
	}
}
```

（Latest 已按新→旧排序，query 过滤保序，不再排序。）

- [ ] **Step 4: 运行确认通过**

Run: `cd backend && go test ./internal/tool/builtin/ -count=1`
Expected: PASS（含既有 builtin/search 测试回归）

- [ ] **Step 5: Commit**

```bash
git add backend/internal/tool/builtin/memory.go backend/internal/tool/builtin/memory_test.go
git commit -m "feat(tool): memory_write/memory_read builtin tools backed by session_memories"
```

---

## Chunk 2: 指令注入 + API + 接线

### Task 3: 顶层 run 指令注入记忆

**Files:**
- Modify: `backend/internal/runtime/runner_eino.go`（RunRequest 加字段；buildAgent/executeNew/executeResume 传参）
- Modify: `backend/internal/runtime/runner_child.go`（buildAgentFiltered 加 memContext 参数）
- Modify: `backend/internal/runtime/runtime.go`（Deps.Memories + run() 计算 memoryBlock）
- Test: `backend/internal/runtime/memory_block_test.go`

- [ ] **Step 1: 写失败测试** `backend/internal/runtime/memory_block_test.go`：

```go
package runtime

import (
	"strings"
	"testing"

	"rivus-agent-backend/internal/domain"
)

func TestComposeMemoryBlockEmpty(t *testing.T) {
	if got := composeMemoryBlock(nil); got != "" {
		t.Fatalf("empty memories must yield empty block, got %q", got)
	}
}

func TestComposeMemoryBlockContent(t *testing.T) {
	got := composeMemoryBlock([]domain.Memory{{Content: "a"}, {Content: "b"}})
	if !strings.Contains(got, "- a") || !strings.Contains(got, "- b") || !strings.Contains(got, "会话记忆") {
		t.Fatalf("missing lines: %q", got)
	}
}

func TestComposeMemoryBlockCap(t *testing.T) {
	mems := make([]domain.Memory, 0, 50)
	for i := 0; i < 50; i++ {
		mems = append(mems, domain.Memory{Content: strings.Repeat("x", 1024)})
	}
	got := composeMemoryBlock(mems)
	if len(got) > 8*1024+200 { // 上限 8KB（留标头余量）
		t.Fatalf("block too large: %d", len(got))
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd backend && go test ./internal/runtime/ -run TestComposeMemory -v`
Expected: FAIL（composeMemoryBlock 未定义）

- [ ] **Step 3: 实现**

`runner_eino.go`：RunRequest 加两个字段：

```go
type RunRequest struct {
	RunID         string
	OwnerID       string
	Goal          string
	Mode          string
	Budget        domain.Budget
	ApprovedTools map[string]bool
	CheckpointID  string
	SessionID     string // 顶层 run 的会话（子 agent 不经 RunRequest）
	MemoryContext string // 预格式化的记忆块（""=无）；由 runtime.run 组装
}
```

`runner_child.go` buildAgentFiltered 加参数（3 个调用点同步：buildAgent、RunChild；`grep -n buildAgentFiltered` 全查）：

```go
func (r *EinoRunner) buildAgentFiltered(ctx context.Context, runID string, budget domain.Budget,
	approved map[string]bool, toolNames []string, depth int, memContext string) (*adk.ChatModelAgent, error) {
```

instruction 拼接处（RLock 段后）：

```go
	r.mu.RLock()
	instr := r.instruction + memContext
	r.mu.RUnlock()
```

`buildAgent` 加同名参数：

```go
func (r *EinoRunner) buildAgent(ctx context.Context, runID string, budget domain.Budget, approved map[string]bool, memContext string) (*adk.ChatModelAgent, error) {
	return r.buildAgentFiltered(ctx, runID, budget, approved, nil, 0, memContext)
}
```

executeNew：`r.buildAgent(ctx, req.RunID, req.Budget, req.ApprovedTools, req.MemoryContext)`。
executeResume：`r.buildAgent(ctx, req.RunID, resolveResumeBudget(req.Budget), req.ApprovedTools, "")`（Resume 死代码路径，不注入）。
RunChild：`r.buildAgentFiltered(ctx, req.ChildRunID, req.Budget, nil, req.ToolNames, req.Depth, "")`（子 agent 不注入 —— 只读执行器，省 token）。

`runtime.go`：Deps 加 `Memories *store.MemoryRepo`（注释：可空=不注入）；run() 在构造 RunRequest 处（`ch, err := r.deps.Runner.Run(ctx, RunRequest{...})`）加：

```go
	MemoryContext: r.memoryBlock(ctx, run.SessionID),
	SessionID:     run.SessionID,
```

runtime.go 末尾（appendBudget 附近）加：

```go
const (
	memoryInjectMax     = 10        // 注入最近条数
	memoryBlockMaxBytes = 8 * 1024  // 记忆块总字节上限
)

func (r *Runtime) memoryBlock(ctx context.Context, sessionID string) string {
	if r.deps.Memories == nil || sessionID == "" {
		return ""
	}
	mems, err := r.deps.Memories.Latest(ctx, sessionID, memoryInjectMax)
	if err != nil || len(mems) == 0 {
		return ""
	}
	return composeMemoryBlock(mems)
}

func composeMemoryBlock(mems []domain.Memory) string {
	var sb strings.Builder
	sb.WriteString("\n\n## 会话记忆（此前沉淀，供参考）\n")
	total := 0
	for _, m := range mems {
		line := "- " + m.Content + "\n"
		if total+len(line) > memoryBlockMaxBytes {
			break
		}
		sb.WriteString(line)
		total += len(line)
	}
	return sb.String()
}
```

（runtime.go 已 import strings/sync 等；补 `context` 已有。）

- [ ] **Step 4: 运行确认通过 + 全 runtime 回归**

Run: `cd backend && go test ./internal/runtime/ -count=1`
Expected: PASS（若 runner_child_test 直接调 buildAgentFiltered 需补 `, ""` 参数）

- [ ] **Step 5: Commit**

```bash
git add backend/internal/runtime/
git commit -m "feat(runtime): inject latest session memories into top-level run instruction"
```

### Task 4: GET /api/v1/sessions/{id}/memories

**Files:**
- Modify: `backend/internal/api/http/server.go`

- [ ] **Step 1: 实现路由**（本任务无独立单测 —— Server 构造需全量依赖，集成测试 Task 5 覆盖；现有 server 测试均走 harness）

routes() 加（sessions 段后）：

```go
	s.mux.HandleFunc("GET /api/v1/sessions/{id}/memories", s.handleListMemories)
```

Server struct 加字段 `memories *store.MemoryRepo`；New() 中（s := &Server{...} 之后）：

```go
	if db != nil {
		s.memories = store.NewMemoryRepo(db)
	}
```

handler（handleDeleteSession 后）：

```go
func (s *Server) handleListMemories(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, _, _, _, err := s.tasks.GetSession(r.Context(), ownerOf(r), id); err != nil {
		writeErr(w, 404, "not_found", "session not found", false)
		return
	}
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	mems, err := s.memories.Latest(r.Context(), id, limit)
	if err != nil {
		writeErr(w, 500, "internal", "list memories failed", true)
		return
	}
	if mems == nil {
		mems = []domain.Memory{}
	}
	writeJSON(w, 200, map[string]any{"memories": mems})
}
```

（按 server.go 实际 import 情况补 `strconv` / `domain`。）

- [ ] **Step 2: 编译确认**

Run: `cd backend && go build ./... && go vet ./...`
Expected: OK

- [ ] **Step 3: Commit**

```bash
git add backend/internal/api/http/server.go
git commit -m "feat(api): GET /api/v1/sessions/{id}/memories endpoint"
```

### Task 5: app/harness 接线 + 集成测试

**Files:**
- Modify: `backend/internal/app/app.go`（注册 MemoryTools）
- Modify: `backend/tests/testutil/harness.go`（镜像 app.go：repo + 注册 + Deps.Memories）
- Test: `backend/tests/integration/memory_test.go`

- [ ] **Step 1: 写失败集成测试** `backend/tests/integration/memory_test.go`（模式参考 budget_consumed_test.go / builtin_tools_test.go）：

```go
package integration_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"rivus-agent-backend/tests/testutil"
)

// run1 写记忆 → API 可查 → run2 memory_read 读回内容（跨 run 持久 + 指令注入链路）。
func TestMemoryAcrossRuns(t *testing.T) {
	h := testutil.New(t)
	ses := h.CreateSession("mem")

	// run1：写记忆
	h.Model.EnqueueToolCall("tc1", "memory_write", `{"content":"用户偏好简洁回复"}`)
	h.Model.EnqueueText("已记住")
	id1, code, _ := h.CreateRun(ses, fixturePayload(t, ses), "mem-1")
	if code != 201 {
		t.Fatalf("create run1: %d", code)
	}
	run1 := h.WaitTerminal(id1, 30*time.Second)
	if run1.Status != "succeeded" {
		t.Fatalf("run1 status=%s summary=%s", run1.Status, run1.ErrorSummary)
	}

	// API：会话记忆可查
	c, body := h.Do("GET", "/api/v1/sessions/"+ses+"/memories", nil, nil)
	if c != 200 || !strings.Contains(string(body), "用户偏好简洁回复") {
		t.Fatalf("GET memories: code=%d body=%s", c, body)
	}

	// run2：读记忆并复述
	h.Model.EnqueueToolCall("tc2", "memory_read", `{}`)
	h.Model.EnqueueText("记忆内容：用户偏好简洁回复")
	id2, code, _ := h.CreateRun(ses, fixturePayload(t, ses), "mem-2")
	if code != 201 {
		t.Fatalf("create run2: %d", code)
	}
	run2 := h.WaitTerminal(id2, 30*time.Second)
	if run2.Status != "succeeded" {
		t.Fatalf("run2 status=%s summary=%s", run2.Status, run2.ErrorSummary)
	}
	var res struct {
		Result string `json:"result"`
	}
	_ = json.Unmarshal([]byte(run2.ResultJSON), &res)
	if !strings.Contains(res.Result, "用户偏好简洁回复") && !strings.Contains(run2.ResultJSON, "用户偏好简洁回复") {
		t.Fatalf("run2 result missing memory content: %q", run2.ResultJSON)
	}
	_ = context.Background()
}
```

（Run 字段名以 domain/task.go 为准：若 ResultJSON 字段不同按实际调整；WaitTerminal 返回 domain.Run 值。）

- [ ] **Step 2: 运行确认失败**

Run: `cd backend && go test ./tests/integration/ -run TestMemoryAcrossRuns -v`
Expected: FAIL（harness 未注册 memory 工具 → tool_not_found → run1 失败）

- [ ] **Step 3: 接线**

`app.go`（TasksTool 注册之后）：

```go
	// 记忆工具（会话级持久记忆，LRU 100 条）。
	memories := store.NewMemoryRepo(db)
	for _, d := range builtin.MemoryTools(memories) {
		if err := registry.Register(d); err != nil {
			return err
		}
	}
```

同时 runtime.New 的 Deps 加 `Memories: memories,`。

`harness.go`（TasksTool 注册之后、runtime.New 之前，镜像 app.go）：

```go
	memories := store.NewMemoryRepo(db)
	for _, d := range builtin.MemoryTools(memories) {
		if err := registry.Register(d); err != nil {
			t.Fatal(err)
		}
	}
	rt := runtime.New(runtime.Deps{Tasks: tasks, Events: events, Approvals: approvals, Runner: runner, Memories: memories}, 4)
```

- [ ] **Step 4: 运行确认通过 + 全集成回归**

Run: `cd backend && go test ./tests/integration/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/app/app.go backend/tests/testutil/harness.go backend/tests/integration/memory_test.go
git commit -m "feat(app): wire memory tools and injection in app and test harness"
```

---

## Chunk 3: 验收

### Task 6: 文档同步 + 全量验收

**Files:**
- Modify: `backend/README.md` / `backend/README.en.md`（工具清单 7→9：memory_write/memory_read；API 表加 GET memories 行 → 20 路由）
- Modify: `README.md` / `README.en.md`（API 速览 19→20 行，若含路由表）

- [ ] **Step 1: 文档更新**（以实际文件为准，先 grep 工具清单与路由表行数）
- [ ] **Step 2: 全量验收**

```bash
cd backend && go build ./... && go vet ./... && go test ./... -count=1 -timeout 600s
cd ../frontend && npx tsc --noEmit && npx vitest run
git status --short   # 除预期改动外干净
```

Expected: 全绿（backend 全包 ok；frontend 0 错误 + 127 测试通过）

- [ ] **Step 3: Commit + 验收报告**

```bash
git add -A backend/README.md backend/README.en.md README.md README.en.md
git commit -m "docs: session memory tools and memories endpoint (zh/en)"
```

验收清单（spec §批4）：
1. migrate v3 记录存在、旧库重复开库不报错（幂等）
2. LRU 100 条裁剪 + 会话删除级联（单测覆盖）
3. memory_write 2KB 上限拒绝；无 TaskContext 报错（单测覆盖）
4. 跨 run 记忆持久（集成测试覆盖）
5. 指令注入 ≤10 条 / 8KB，子 agent 不注入
6. GET memories 端点 owner 隔离（非本人 404）
7. build/vet/test + frontend 全绿
