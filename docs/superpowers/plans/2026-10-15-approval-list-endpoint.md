# 审批读端点 `GET /runs/{id}/approvals` Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增只读端点 `GET /api/v1/runs/{id}/approvals`，返回某 run 的全量审批记录（`[]Approval`，按 `created_at` 升序，空时为 `[]` 非 `null`），供前端 bootstrap pending 审批卡；含跨租户归属校验。

**Architecture:** 纯后端、3 个文件。`run_service.go` 加 `ListApprovals(ctx, ownerID, runID)`——先 `tasks.GetRun` 做租户归属校验（镜像 `DecideApproval` 的 GetRun 先行模式），再 `approvals.ListByRun(runID)`。`server.go` 加 1 条路由 + 1 个 handler：404 判定用 `sql.ErrNoRows`（`GetRun` 对不存在的 run 直接返回 `QueryRowContext` 的原始 `sql.ErrNoRows`，**非** `domain.ErrNotFound`）或 `domain.ErrForbidden`（非 owner）；其余错误 500 `retryable`。前端零改动。

**Tech Stack:** Go（`backend/`），集成测试 `backend/tests/integration/` + `testutil.Harness`。

**Spec:** `docs/superpowers/specs/2026-10-15-approval-list-endpoint-design.md`

**Out of scope（明确不做）:** 分页 / 状态过滤 / `?status=pending`；前端 `api.listApprovals` + 气泡接线；改 `Server` 构造签名（`runs *service.RunService` 已在 `server.go:32`）。

---

## File Structure

- Modify: `backend/internal/service/run_service.go` — 新增 `ListApprovals` 方法（加在 `DecideApproval` 附近，`run_service.go:139`）。
- Modify: `backend/internal/api/http/server.go` — `routes()`（line 47-62）加 1 行路由；新增 `handleListApprovals`。
- Test: `backend/tests/integration/approval_list_test.go`（新建，`package integration`，复用 `publishToolDef`/`fixturePayload`/`waitApproval`/`contains`）。

## Chunk 1: Service

### Task 1: `RunService.ListApprovals`

**Files:**
- Modify: `backend/internal/service/run_service.go`（`RunService` 已持 `tasks *store.TaskRepo` + `approvals *store.ApprovalRepo`，见 `run_service.go:19-21`）

- [ ] **Step 1: 实现方法**

在 `DecideApproval`（`run_service.go:139`）附近加（无需新 import；`context`/`domain`/`store` 已在）：

```go
// ListApprovals 返回某 run 的全部审批记录（按 created_at 升序），供前端 bootstrap pending 卡。
func (s *RunService) ListApprovals(ctx context.Context, ownerID, runID string) ([]domain.Approval, error) {
	// 先做租户归属校验：不存在的 run（GetRun 返回 sql.ErrNoRows）或非本 owner 的
	// run（GetRun 返回 domain.ErrForbidden）都直接返回，防越权读他人审批。
	if _, err := s.tasks.GetRun(ctx, ownerID, runID); err != nil {
		return nil, err
	}
	return s.approvals.ListByRun(ctx, runID)
}
```

（`ListByRun` 签名：`(ctx context.Context, runID string) ([]domain.Approval, error)`，`approval_repo.go:62`；其内部 `out := []domain.Approval{}` 初始化保证空时返回 `[]` 非 `nil`。）

- [ ] **Step 2: 编译确认**

Run: `cd backend && go build ./internal/service/`
Expected: 编译通过。

- [ ] **Step 3: Commit**

```bash
git add backend/internal/service/run_service.go
git commit -m "feat(service): add RunService.ListApprovals with owner tenancy check"
```

## Chunk 2: Endpoint

### Task 2: 路由 + handler（`server.go`）

**Files:**
- Modify: `backend/internal/api/http/server.go` — `routes()` line 47-62 加 1 行；新增 `handleListApprovals`。`server.go` 已 import `database/sql`/`errors`/`domain`（line 5,7,15），无需改 import。

- [ ] **Step 1: 加路由**

在 `routes()` 的 `s.mux.HandleFunc("POST /api/v1/runs/{id}/approvals/{approval_id}", s.handleApproval)`（line 58）后加：

```go
	s.mux.HandleFunc("GET /api/v1/runs/{id}/approvals", s.handleListApprovals)
```

- [ ] **Step 2: 加 handler**

参照 `handleGetRun`（line 217-230）风格，加：

```go
func (s *Server) handleListApprovals(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	approvals, err := s.runs.ListApprovals(r.Context(), ownerOf(r), id)
	if err != nil {
		// GetRun 对不存在的 run 返回 sql.ErrNoRows（非 domain.ErrNotFound），
		// 对非本 owner 的 run 返回 domain.ErrForbidden；两者都走 404，
		// 避免泄露他人 run 是否存在。ListByRun 的 DB 读失败仍走 500。
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, domain.ErrForbidden) {
			writeErr(w, 404, "not_found", "run not found", false)
			return
		}
		writeErr(w, 500, "internal", "list approvals failed", true)
		return
	}
	writeJSON(w, 200, map[string]any{"approvals": approvals})
}
```

- [ ] **Step 3: 编译确认**

Run: `cd backend && go build ./internal/api/http/`
Expected: 编译通过。

- [ ] **Step 4: Commit**

```bash
git add backend/internal/api/http/server.go
git commit -m "feat(http): add GET /runs/{id}/approvals read endpoint"
```

## Chunk 3: Integration Test

### Task 3: 4 用例集成测试

**Files:**
- Test: `backend/tests/integration/approval_list_test.go`（新建，`package integration`）

- [ ] **Step 1: 写测试**

`h.Model.SetFallback` 会覆盖 `ScriptedModel.fallback`（`harness.go:75`），且 `Generate` 先检查 `fallback != nil`（`harness.go:90`）——所以每个用例用一次新的 `SetFallback` 控制模型行为。用例 2（无审批）用**不触发 tool call** 的 fallback，使 run 直接 `succeeded`、无 pending 记录。

```go
package integration

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/tests/testutil"
)

// TestApprovalListEndpoint 覆盖 GET /runs/{id}/approvals 的 200/空/404/跨租户。
func TestApprovalListEndpoint(t *testing.T) {
	h := testutil.New(t, publishToolDef())

	// 造一个触发 waiting_approval 的 run（SetFallback body 与 approval_test.go:49-59 一致：
	// 带 tool call 触发审批，收到 Tool 结果后给 final 证据文本）。
	finalText := "输出风险列表：已发布（证据：publish_draft 结果），每条建议有文件或代码证据。"
	h.Model.SetFallback(func(_ int, input []*schema.Message) (*schema.Message, error) {
		for _, m := range input {
			if m.Role == schema.Tool {
				return &schema.Message{Role: schema.Assistant, Content: finalText}, nil
			}
		}
		return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
			ID: "tc1", Type: "function",
			Function: schema.FunctionCall{Name: "publish_draft", Arguments: `{"title":"hello"}`},
		}}}, nil
	})
	ses := h.CreateSession("s")
	id, code, _ := h.CreateRun(ses, fixturePayload(t, ses), "list-1")
	if code != 201 {
		t.Fatalf("create run: %d", code)
	}
	a := waitApproval(t, h, id) // approval_test.go:30，造出 1 条 pending 记录

	// 用例 1：审批后的 run → 200，approvals 含 1 条，字段与 waitApproval 记录一致
	_, body := h.Do("GET", "/api/v1/runs/"+id+"/approvals", nil, nil)
	got := parseApprovals(t, body)
	if len(got) != 1 || got[0].ID != a.ID {
		t.Fatalf("want 1 approval %s, got %+v", a.ID, got)
	}
	if got[0].ToolName != a.ToolName || got[0].Status != a.Status {
		t.Fatalf("approval fields mismatch: %+v", got[0])
	}

	// 用例 2：无审批的 run → 200 {"approvals":[]}（非 null）。
	// 换掉 fallback：不触发 tool call，直接给 final 证据 → run succeeded、无 pending 记录。
	h.Model.SetFallback(func(_ int, _ []*schema.Message) (*schema.Message, error) {
		return &schema.Message{Role: schema.Assistant, Content: "done（证据：x）"}, nil
	})
	ses2 := h.CreateSession("s2")
	id2, code2, _ := h.CreateRun(ses2, fixturePayload(t, ses2), "list-2")
	if code2 != 201 {
		t.Fatalf("create run2: %d", code2)
	}
	_ = h.WaitTerminal(id2, 30*time.Second)
	_, body2 := h.Do("GET", "/api/v1/runs/"+id2+"/approvals", nil, nil)
	if !contains(string(body2), `"approvals":[]`) {
		t.Fatalf("want empty approvals [], got %s", body2)
	}

	// 用例 3：不存在的 run id → 404
	code3, _ := h.Do("GET", "/api/v1/runs/does-not-exist/approvals", nil, nil)
	if code3 != 404 {
		t.Fatalf("want 404 for unknown run, got %d", code3)
	}

	// 用例 4：非 owner 读他人 run → 404（跨租户校验，delete_session_test.go:21 先例）
	code4, _ := h.Do("GET", "/api/v1/runs/"+id+"/approvals", nil, map[string]string{"X-Owner-ID": "intruder"})
	if code4 != 404 {
		t.Fatalf("want 404 for cross-tenant, got %d", code4)
	}
}

func parseApprovals(t *testing.T, body []byte) []domain.Approval {
	t.Helper()
	var out struct {
		Approvals []domain.Approval `json:"approvals"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("parse approvals: %v", err)
	}
	return out.Approvals
}
```

（`time` import 供 `WaitTerminal`；`schema` import 供 `SetFallback` body；`publishToolDef`/`fixturePayload`/`waitApproval`/`contains` 复用 `approval_test.go`/`api_test.go` 的既有 helper；`X-Owner-ID` header 见 `server.go:66` `ownerOf`。）

- [ ] **Step 2: 运行确认通过**

Run: `cd backend && go test ./tests/integration/ -run TestApprovalListEndpoint -v`
Expected: PASS。

- [ ] **Step 3: vet + 全量后端测试**

Run: `cd backend && go vet ./... && go test ./tests/integration/`
Expected: 全绿。

- [ ] **Step 4: Commit**

```bash
cd backend && gofmt -w tests/integration/approval_list_test.go
git add backend/tests/integration/approval_list_test.go
git commit -m "test(integration): approval list endpoint 200/empty/404/cross-tenant"
```

## Chunk 4: Acceptance

- [ ] **Step 1: 后端全量**

Run: `cd backend && go build ./... && go vet ./... && go test ./tests/integration/`
Expected: 全绿。

- [ ] **Step 2: 前端零改动验收**

Run: `git diff --name-only`
Expected: 不含 `frontend/`（本 spec 纯后端）。

- [ ] **Step 3: 端点验收对照**

- `GET /runs/{id}/approvals` 200/404 行为正确；返回全字段 `[]Approval`；空时为 `[]` 非 `null`。
- 租户归属校验生效（非 owner 读他人 run → 404，经 `domain.ErrForbidden`；不存在的 run → 404，经 `sql.ErrNoRows`）。
- 前端零改动。
