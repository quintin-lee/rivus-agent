# Round-17 会话重命名 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 会话标题可改：后端 `PATCH /api/v1/sessions/{id}` + 前端 SessionList 行内改名。

**Architecture:** repo（属主 guard + UPDATE）→ service（trim/空拒绝/200 截断，哨兵 ErrEmptyTitle）→ handler（400/404/200 回读）；前端 api + store action + SessionList 行内 Input（Enter/blur 提交、Esc 取消）。

**Tech Stack:** Go net/http PathValue、SQLite、zustand、vitest + @testing-library/react。

**已验证事实：** GetSession 属主不匹配 → `domain.ErrForbidden`，不存在 → sql.ErrNoRows（handler 均映射 404，沿用现有写法）；`nowMs()` 在 task_repo.go:17；SessionService.Create L18 有 trim/默认/截断先例；SessionList 行结构 = 标题按钮 + Brain + Trash2（group-hover 隐藏）。

---

### Task 1: store.RenameSession + service.Rename（TDD）

**Files:**
- Modify: `backend/internal/store/task_repo.go`（DeleteSession 后插入）、`backend/internal/store/task_repo_test.go`
- Modify: `backend/internal/service/session_service.go`

- [ ] **Step 1: RED — store 测试**

```go
func TestRenameSession(t *testing.T) {
	r := newRepo(t) // 沿用本文件现有 helper（setupRun 同款 db）
	ctx := context.Background()
	id, err := r.CreateSession(ctx, "u1", "old")
	if err != nil {
		t.Fatal(err)
	}
	before, err := r.GetSession(ctx, "u1", id)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond) // ms 精度：确保 updated_at 可观测推进
	if err := r.RenameSession(ctx, "u1", id, "new-title"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	owner, title, _, ua, err := r.GetSession(ctx, "u1", id)
	if err != nil || title != "new-title" {
		t.Fatalf("get after rename: title=%q err=%v", title, err)
	}
	_ = owner
	if ua < before[3] { // before = (owner,title,ca,ua)
		t.Fatalf("updated_at went backwards: %d < %d", ua, before[3])
	}
	// 属主不匹配 → ErrForbidden（GetSession 语义）
	if err := r.RenameSession(ctx, "someone-else", id, "x"); err != domain.ErrForbidden {
		t.Fatalf("owner mismatch err = %v", err)
	}
	// 不存在 → sql.ErrNoRows
	if err := r.RenameSession(ctx, "u1", "ses_missing", "x"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing err = %v", err)
	}
}
```

（按本文件现有 helper 名微调：若无 newRepo 就用现有 Open(t.TempDir()) 模式。）

- [ ] **Step 2: 运行确认失败** `cd backend && go test ./internal/store/ -run TestRenameSession -count=1` → FAIL（undefined: RenameSession）

- [ ] **Step 3: 实现**

```go
// RenameSession 更新会话标题（属主校验沿用 GetSession，updated_at 推进到 now）。
func (r *TaskRepo) RenameSession(ctx context.Context, ownerID, sessionID, title string) error {
	if _, _, _, _, err := r.GetSession(ctx, ownerID, sessionID); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE sessions SET title = ?, updated_at = ? WHERE id = ? AND owner_id = ?`,
		title, nowMs(), sessionID, ownerID)
	return err
}
```

- [ ] **Step 4: GREEN + service**

service（哨兵 + 规则）：

```go
var ErrEmptyTitle = errors.New("title is empty")

func (s *SessionService) Rename(ctx context.Context, ownerID, sessionID, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return ErrEmptyTitle
	}
	if len(title) > 200 {
		title = title[:200]
	}
	return s.tasks.RenameSession(ctx, ownerID, sessionID, title)
}
```

Run: `go build ./... && go test ./internal/store/ ./internal/service/ -count=1` → PASS

- [ ] **Step 5: Commit** `feat(store): RenameSession with owner guard; service trims and rejects empty title`

### Task 2: PATCH handler + 集成测试（TDD）

**Files:**
- Modify: `backend/internal/api/http/handlers_sessions.go`、`backend/internal/api/http/routes.go`
- Create: `backend/tests/integration/session_rename_test.go`

- [ ] **Step 1: RED — 集成测试**

```go
package integration

import (
	"net/http"
	"testing"

	"rivus-agent-backend/tests/testutil"
)

func TestSessionRename(t *testing.T) {
	h := testutil.New(t)
	ses := h.CreateSession("old-name")

	// 改名 → 200 回读
	code, body := h.Do(http.MethodPatch, "/api/v1/sessions/"+ses,
		map[string]string{"title": "renamed"}, nil)
	if code != 200 {
		t.Fatalf("patch: %d %s", code, body)
	}
	run, _ := h.GetRun /* 占位，不用 */ (t.Name())
	_ = run
	got := h.Do2(http.MethodGet, "/api/v1/sessions/"+ses, nil, nil) // 见偏差：用 h.Do
	_ = got
	// ……断言 body 含 "renamed"（实现时用 h.Do 两值返回）

	// 空 title → 400 bad_request
	code2, body2 := h.Do(http.MethodPatch, "/api/v1/sessions/"+ses,
		map[string]string{"title": "  "}, nil)
	if code2 != 400 {
		t.Fatalf("empty title: %d %s", code2, body2)
	}

	// 不存在 → 404
	code3, _ := h.Do(http.MethodPatch, "/api/v1/sessions/ses_missing",
		map[string]string{"title": "x"}, nil)
	if code3 != 404 {
		t.Fatalf("missing: %d", code3)
	}
}
```

（骨架仅示意；实现时用 h.Do 两值返回与 strings.Contains 断言。）

- [ ] **Step 2: 运行确认失败** `go test ./tests/integration/ -run TestSessionRename -count=1` → FAIL（405/404）

- [ ] **Step 3: 实现 handler + 路由**

```go
type renameSessionReq struct {
	Title string `json:"title"`
}

func (s *Server) handleRenameSession(w http.ResponseWriter, r *http.Request) {
	var in renameSessionReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		writeErr(w, 400, "bad_request", "invalid body", false)
		return
	}
	if err := s.sessions.Rename(r.Context(), ownerOf(r), r.PathValue("id"), in.Title); err != nil {
		if errors.Is(err, service.ErrEmptyTitle) {
			writeErr(w, 400, "bad_request", "title is empty", false)
			return
		}
		writeErr(w, 404, "not_found", "session not found", false)
		return
	}
	owner, title, ca, ua, err := s.tasks.GetSession(r.Context(), ownerOf(r), r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, "not_found", "session not found", false)
		return
	}
	_ = owner
	writeJSON(w, 200, map[string]any{"session_id": r.PathValue("id"), "title": title, "created_at": ca, "updated_at": ua})
}
```

routes.go 加 `s.mux.HandleFunc("PATCH /api/v1/sessions/{id}", s.handleRenameSession)`（放 DELETE 行后）。imports：`errors` + `service`（若未导入）。

- [ ] **Step 4: GREEN + 回归** `go build ./... && go vet ./... && go test ./tests/integration/ -run TestSessionRename -count=1` → PASS；`go test ./... -count=1` 全绿。

- [ ] **Step 5: Commit** `feat(http): PATCH /sessions/{id} renames a session with owner guard`

### Task 3: 前端 api + store + 行内改名（TDD）

**Files:**
- Modify: `frontend/src/lib/api.ts`、`frontend/src/store/session-store.ts`、`frontend/src/components/SessionList.tsx`、`frontend/src/i18n/index.ts`
- Test: `frontend/src/components/SessionList.test.tsx`

- [ ] **Step 1: RED — SessionList 测试**

在 SessionList.test.tsx 追加（沿用该文件既有 api mock 手法）：

```tsx
it('renames a session inline: pencil opens input, enter commits', async () => {
  // sessions 预置 {session_id:'s1', title:'Old', ...}；api.renameSession mock resolve
  fireEvent.click(screen.getByTitle('重命名会话'))
  const input = screen.getByDisplayValue('Old')
  fireEvent.change(input, { target: { value: 'New' } })
  fireEvent.keyDown(input, { key: 'Enter' })
  await vi.waitFor(() => expect(api.renameSession).toHaveBeenCalledWith('s1', 'New'))
})

it('esc cancels inline rename without calling api', () => {
  fireEvent.click(screen.getByTitle('重命名会话'))
  fireEvent.keyDown(screen.getByDisplayValue('Old'), { key: 'Escape' })
  expect(api.renameSession).not.toHaveBeenCalled()
  expect(screen.getByText('Old')).toBeTruthy()
})
```

- [ ] **Step 2: 运行确认失败** `cd frontend && npx vitest run src/components/SessionList.test.tsx` → FAIL（找不到铅笔按钮）

- [ ] **Step 3: 实现**

api.ts：

```ts
async renameSession(id: string, title: string): Promise<Session> {
  return this.request<Session>('PATCH', `/api/v1/sessions/${id}`, { title })
}
```

（沿用该文件 request 封装/路径写法——实现时对照 deleteSession 相邻代码。）

session-store.ts：state 接口 + 实现

```ts
renameSession: async (id, title) => {
  const s = await api.renameSession(id, title)
  set((state) => ({
    sessions: state.sessions.map((x) =>
      x.session_id === id ? { ...x, title: s.title, updated_at: s.updated_at } : x),
  }))
},
```

SessionList.tsx：新增 `const renameSession = useSessionStore((s) => s.renameSession)`；状态 `const [renamingId, setRenamingId] = useState<string | null>(null)`、`const [draft, setDraft] = useState('')`。行渲染：`renamingId === s.session_id` 时标题按钮换成 `<Input autoFocus value={draft} onChange… onKeyDown={(e)=>{ if(e.key==='Enter') void commit(); if(e.key==='Escape'){ setRenamingId(null) } }} onBlur={() => void commit()} className="h-6 text-xs" />`；否则标题按钮 + 新 Pencil 按钮（title={t('sessionlist.renameTitle')}，className 与 Brain 一致，onClick 进入改名态）。commit：trim 空 → 取消；否则 `await renameSession(renamingId, draft.trim())` 后 `setRenamingId(null)`。i18n zh `'sessionlist.renameTitle': '重命名会话'`、en `'Rename session'`。

- [ ] **Step 4: GREEN + 回归** `npx tsc --noEmit && npm run lint && npx vitest run` → 全绿（179+2）

- [ ] **Step 5: Commit** `feat(frontend): inline session rename via pencil entry`

### Task 4: 文档 + 全量验收

- [ ] README.md / README.en.md API 表 +1 行 `| PATCH | /api/v1/sessions/{id} | 会话重命名 / Rename a session |`（22 行）。
- [ ] docs/api/sessions.md + docs/en/api/sessions.md 新增 `## PATCH /api/v1/sessions/{id}`（请求 `{title}`、200/400/404、curl 示例）。
- [ ] 全量：backend go build/vet/test + make test-eval；frontend tsc/lint/vitest。
- [ ] spec §7 偏差回写 + commit `docs(round17): rename API docs (zh/en) + spec deviations`。

## 自检（self-review）

- GetSession 双错误语义（ErrForbidden/ErrNoRows）handler 都映射 404 —— 与现有 handleGetSession 一致，不区分 403。
- before[3] 索引取 ua：实现时可改为命名返回更清晰（测试里直接两次 GetSession 比较）。
- h.Do 返回两值 `(int, []byte)`；集成测试不用 GetRun 占位（骨架笔误，实现时删除）。
- Input onBlur 与 Enter 双触发风险：Enter 提交后 setRenamingId(null) 卸载 Input，blur 不再触发；Esc 先置空 renamingId 即可。若实测双调，用 `if (!renamingId) return` 防御。
