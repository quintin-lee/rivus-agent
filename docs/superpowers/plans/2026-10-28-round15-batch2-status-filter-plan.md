# Round-15 批2：运行历史状态过滤实施计划

> **For agentic workers:** REQUIRED: Use superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `GET /api/v1/runs` 支持可选 `status` 过滤（owner 全局与会话内两分支），RunsHistoryDialog 加筛选 chips。

**Architecture:** repo 两函数加 `status string` 参数（空=不过滤，SQL 条件拼接）；handler 校验 RunStatus 合法性（domain 新增 `Valid()`）；前端 api.listRuns 加可选 status 参 + dialog chips 重拉。

**Tech Stack:** Go 1.27 / net/http / SQLite（idx_runs_status 就绪）/ React + vitest。

**Spec:** docs/superpowers/specs/2026-10-28-round15-i18n-filter-evals-design.md §3.2

---

## Chunk 1

### Task 1: store 层 status 过滤（TDD）

**Files:**
- Modify: `backend/internal/store/task_repo.go`（ListRunsBySession:219 / ListRunsByOwner:270 签名 + SQL）
- Modify: `backend/internal/domain/task.go`（新增 `func (s RunStatus) Valid() bool`）
- Test: `backend/internal/store/task_repo_test.go` 追加 TestListRunsFilterByStatus

- [ ] **Step 1: 失败测试**——setupRun 风格种 3 个 run（succeeded/failed/cancelled）：断言 `ListRunsByOwner(ctx,"u1","failed",0)` 只回 failed；`""` 回全部；`ListRunsBySession` 同断言；导出调用方语义（"" = 原行为）。
- [ ] **Step 2**: `go test ./internal/store/ -run TestListRunsFilterByStatus -count=1` → 编译错（参数不匹配）= RED。
- [ ] **Step 3: 实现**——两函数签名 `(…, status string, limit int)`；SQL 改条件拼接（args []any，status != "" 时追加 `AND status = ?`）；domain 加 `Valid()` switch 8 状态。
- [ ] **Step 4**: 同测试 GREEN + `go build ./...`（export.go:46 调用点改传 ""）。
- [ ] **Step 5: Commit** `feat(store): status filter for ListRunsByOwner/ListRunsBySession`

### Task 2: handler 校验 + 集成断言（TDD）

**Files:**
- Modify: `backend/internal/api/http/handlers_runs.go`（handleListRuns 读 status → Valid() 校验 → 透传）
- Test: `backend/tests/integration/runs_history_test.go` 追加 2 例（非法 status 400；`?status=failed` 只回失败 run）

- [ ] **Step 1: 失败测试**——TestListRunsStatusInvalid（GET /api/v1/runs?status=bogus → 400 bad_request）；TestListRunsStatusFilter（2 个 run 一败一成 → `?status=failed` 仅败者；无参两者都有）。
- [ ] **Step 2**: `go test ./tests/integration/ -run TestListRunsStatus -count=1` → RED（无过滤=全回/200）。
- [ ] **Step 3: 实现**——handleListRuns：`status := q.Get("status")`；非空且 `!domain.RunStatus(status).Valid()` → writeErr 400；两分支调用传 status。
- [ ] **Step 4**: GREEN + `go vet ./...` + gofmt。
- [ ] **Step 5: Commit** `feat(http): optional status filter on GET /runs`

### Task 3: 前端 chips（TDD）

**Files:**
- Modify: `frontend/src/lib/api.ts`（listRuns 第三可选参 status）
- Modify: `frontend/src/components/RunsHistoryDialog.tsx`（statusFilter state + chips 行 + 重拉）
- Modify: `frontend/src/i18n/index.ts`（`runs.filterAll` 全部/All）
- Test: `frontend/src/lib/api.test.ts` + `frontend/src/components/RunsHistoryDialog.test.tsx`

- [ ] **Step 1: 失败测试**——api.test.ts 2 例（status 拼参 / 不传不拼）；RunsHistoryDialog.test.tsx 1 例（点击 失败 chip → listRuns 被调 with (undefined, 50, 'failed')）。
- [ ] **Step 2**: `npx vitest run src/lib/api.test.ts src/components/RunsHistoryDialog.test.tsx` → RED。
- [ ] **Step 3: 实现**——api.ts：`const qs = sessionId ? \`session_id=…&limit=…\` : \`limit=…\`; const path = status ? \`…&status=${status}\` : …`（not_found 兜底不变）；Dialog：`const [statusFilter, setStatusFilter] = useState('')`，chips 数组 `[['', t('runs.filterAll')], ['succeeded', t('status.succeeded')], …]`，点击 set + 重拉（load 复用 statusFilter，用函数式或直接传参）；i18n 加 `runs.filterAll`。
- [ ] **Step 4**: `npx tsc --noEmit && npx vitest run` 全绿。
- [ ] **Step 5: Commit** `feat(frontend): status filter chips in run history dialog`

## Self-review 备注

- repo 条件拼接注意 args 顺序（status 在 owner/session 之后、limit 之前）。
- handler 400 用现有 writeErr 形状 `{code:"bad_request",…,retryable:false}`。
- 前端 chips 复用 R13 的 `status.*` 8 状态键（succeeded/failed/cancelled/timed_out 4 个 + filterAll）。
- RunsHistoryDialog load() 现签名 `api.listRuns(undefined, 50)` → 改读 statusFilter state；refresh 按钮同一 load。
