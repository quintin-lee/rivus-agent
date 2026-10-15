# 审批事件 payload 打通 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 给 `approval.requested` / `approval.decided` 两条事件补上 `approval_id` + `tool_name`（decided 再加 `approved` 真 bool + `reason`），让前端（零改动）能把 pending 审批卡渲染出来并调通 `decideApproval`。

**Architecture:** 纯后端、2 个文件 + 1 个测试文件。权威发射点 `runtime.go` 的 `onNeedApproval` handler 改为"先 `Approvals.Create` 拿到 `a.ID`、成功才发事件"，新增 `appendApproval` helper 发三字段 payload；`run_service.go` 的 decided payload 从字符串拼接 `boolStr` 换成 `json.Marshal(map[string]any{...})`（`approved` 必须是 JSON bool）并删除死代码 `boolStr`。前端按这些 key 预写（`parseApprovalPayload` / `ApprovalCard.parseApproval`），零改动即复活。

**Tech Stack:** Go（`backend/`），集成测试 `backend/tests/integration/approval_test.go` + `testutil.Harness`。

**Spec:** `docs/superpowers/specs/2026-10-15-approval-payload-design.md`

**Out of scope（明确不做）:** 副信号路径 `runner_eino.go:277/302`、显式 expired 决策、`GET /runs/{id}/approvals` 读端点、前端任何改动、新日志手段。

---

## File Structure

- Modify: `backend/internal/runtime/runtime.go:87-101` — `onNeedApproval` handler：`Approvals.Create` 成功才发事件；`backend/internal/runtime/runtime.go:166` 附近 — 新增 `appendApproval` helper。
- Modify: `backend/internal/service/run_service.go:158` — decided payload 改 `json.Marshal`；新增 `encoding/json` import；删除 `boolStr`（line 166）。
- Test: `backend/tests/integration/approval_test.go` — 新增 `findEventPayload` helper + 在 `TestApprovalApproveResume` / `TestApprovalReject` 加 payload 断言。

---

## Chunk 1: 打通审批事件 payload

### Task 1: 测试脚手架 + requested payload 断言（先红）

**Files:**
- Modify: `backend/tests/integration/approval_test.go`

- [ ] **Step 1: 加 `findEventPayload` helper**

在 `approval_test.go`（`contains` 函数附近，文件尾部）新增。测试文件已 import `context`、`encoding/json`、`domain`、`testutil`，无需改 import：

```go
// findEventPayload 拉某 run 的指定类型事件，解析其 payload_json 为 map。
func findEventPayload(t *testing.T, h *testutil.Harness, runID string, typ domain.EventType) map[string]any {
	t.Helper()
	evs, err := h.Events.ListAfter(context.Background(), runID, 0, 500)
	if err != nil {
		t.Fatalf("ListAfter: %v", err)
	}
	for _, e := range evs {
		if e.Type == typ {
			var m map[string]any
			if err := json.Unmarshal([]byte(e.PayloadJSON), &m); err != nil {
				t.Fatalf("unmarshal %s payload %q: %v", typ, e.PayloadJSON, err)
			}
			return m
		}
	}
	t.Fatalf("no %s event found for run %s", typ, runID)
	return nil
}
```

- [ ] **Step 2: 在 `TestApprovalApproveResume` 加 requested payload 断言**

`TestApprovalApproveResume`（`approval_test.go:46`）里，`a := waitApproval(t, h, id)`（line 66）之后、`h.Do("POST", ...)` 决策调用之前，插入：

```go
	req := findEventPayload(t, h, id, domain.EvtApprovalRequested)
	if req["approval_id"] != a.ID {
		t.Fatalf("requested payload approval_id = %v, want %s", req["approval_id"], a.ID)
	}
	if req["tool_name"] != a.ToolName {
		t.Fatalf("requested payload tool_name = %v, want %s", req["tool_name"], a.ToolName)
	}
```

（说明：`req["approval_id"]` 是 `any`，`a.ID` 是 `string`——interface 与具体类型比较是合法的，底层类型不符返回 false 而非 panic，故此处安全。decided 的 bool 断言因涉及 untyped 常量，见 Task 3，必须用类型断言。）

- [ ] **Step 3: 在 `TestApprovalReject` 加 requested payload 断言**

`TestApprovalReject`（`approval_test.go:82`）里，`a := waitApproval(t, h, id)`（line 88）之后、决策调用之前，插入同一段 requested 断言（`domain.EvtApprovalRequested`，比较 `a.ID` / `a.ToolName`）。

- [ ] **Step 4: 跑测试，确认 requested 断言失败**

Run: `cd backend && go test ./tests/integration/ -run 'TestApproval' -v`
Expected: FAIL — 当前 `approval.requested` payload 是 `{"text":"publish_draft requires approval"}`，`approval_id`/`tool_name` 字段缺失，`req["approval_id"]` 为 nil ≠ `a.ID`，两个测试均红。

- [ ] **Step 5: 此时先不 commit**（实现未落地，测试应红；commit 等 Task 2 落地后一起进行）

### Task 2: `runtime.go` — requested payload 落地

**Files:**
- Modify: `backend/internal/runtime/runtime.go:87-101,166`

- [ ] **Step 1: 改 handler（`runtime.go:87-101`）**

现状（`_ =` 吞错 + 内联发事件）：

```go
	if _, err := r.deps.Approvals.FindPendingByToolCall(bg, runID, toolCallID); err == nil {
		return
	}
	_ = r.deps.Approvals.Create(bg, &domain.Approval{
		RunID: runID, ToolCallID: toolCallID, ToolName: toolName,
		ArgsHash: itool.ArgsHash(args), RequestedBy: ownerID,
		Reason:    "high-risk tool call requires approval",
		ExpiresAt: time.Now().Add(30 * time.Minute).UnixMilli(),
	})
	r.append(bg, runID, domain.EvtApprovalRequested, toolName+" requires approval")
```

改为（先建记录拿 `a.ID`，Create 成功才发事件；`FindPendingByToolCall` 守卫保留不动）：

```go
	if _, err := r.deps.Approvals.FindPendingByToolCall(bg, runID, toolCallID); err == nil {
		return
	}
	a := &domain.Approval{
		RunID: runID, ToolCallID: toolCallID, ToolName: toolName,
		ArgsHash: itool.ArgsHash(args), RequestedBy: ownerID,
		Reason:    "high-risk tool call requires approval",
		ExpiresAt: time.Now().Add(30 * time.Minute).UnixMilli(),
	}
	if err := r.deps.Approvals.Create(bg, a); err == nil {
		r.appendApproval(bg, runID, a)
	}
```

（说明：`Create` 在 `approval_repo.go:16` 先把 `a.ID = newID("apr")` 填到调用方指针上再 INSERT，故 `Create` 返回后 `a.ID` 可用。Create 失败则静默不发事件——该文件既有惯例是 `_ =` 吞错、无 logger，不新增日志手段。）

- [ ] **Step 2: 加 `appendApproval` helper（`runtime.go:166` 现有 `append` 之后）**

```go
// appendApproval 发带 approval_id + tool_name 的 approval.requested 事件。
func (r *Runtime) appendApproval(ctx context.Context, runID string, a *domain.Approval) {
	payload, _ := json.Marshal(map[string]string{
		"text":        a.ToolName + " requires approval",
		"approval_id": a.ID,
		"tool_name":   a.ToolName,
	})
	_, _ = r.deps.Events.Append(ctx, runID, domain.EvtApprovalRequested, string(payload), "normal")
}
```

（`runtime.go` 已 import `encoding/json`（line 5）与 `context`（line 4），无需新增 import。）

- [ ] **Step 3: 跑测试，确认 requested 断言转绿**

Run: `cd backend && go test ./tests/integration/ -run 'TestApproval' -v`
Expected: PASS — requested payload 现含 `approval_id`/`tool_name`，Task 1 的断言通过（decided 断言尚未加，不影响）。

- [ ] **Step 4: 编译 + commit**

Run: `cd backend && go build ./...`（确认无编译错误）
```bash
git add backend/internal/runtime/runtime.go backend/tests/integration/approval_test.go
git commit -m "feat(approval): enrich approval.requested payload with approval_id+tool_name"
```

### Task 3: 测试加 decided payload 断言（先红）

**Files:**
- Modify: `backend/tests/integration/approval_test.go`

- [ ] **Step 1: `TestApprovalApproveResume` 加 decided 断言**

在 approve 的 `h.Do("POST", ...)` 成功之后、`run := h.WaitTerminal(...)`（line 72）之前，插入：

```go
	dec := findEventPayload(t, h, id, domain.EvtApprovalDecided)
	approved, ok := dec["approved"].(bool)
	if !ok || approved != true {
		t.Fatalf("decided payload approved = %T %v, want bool true", dec["approved"], dec["approved"])
	}
	if dec["approval_id"] != a.ID {
		t.Fatalf("decided payload approval_id = %v, want %s", dec["approval_id"], a.ID)
	}
	if dec["tool_name"] != a.ToolName {
		t.Fatalf("decided payload tool_name = %v, want %s", dec["tool_name"], a.ToolName)
	}
	if dec["reason"] != "looks good" {
		t.Fatalf("decided payload reason = %v, want \"looks good\"", dec["reason"])
	}
```

（关键：`dec["approved"].(bool)` 类型断言把"approved 必须是 JSON bool、不能是字符串 `"true"`"锁死——防止日后退回字符串写法导致前端 `p.approved === true` 判成 rejected。注意：**现状 payload 其实是 `{"approved":true}`（`boolStr` 拼出的 JSON 布尔，`.(bool)` 断言在旧 payload 上本就会通过）**；本步骤转红的真正原因是**缺 `approval_id`/`tool_name`/`reason` 三个字段**（`dec["approval_id"]` 为 nil ≠ `a.ID` 触发 `t.Fatalf`），而 `.(bool)` 断言是对"类型必须为 bool"的防回归保险。）

- [ ] **Step 2: `TestApprovalReject` 加 decided 断言**

在 reject 的 `h.Do("POST", ...)` 成功之后插入：

```go
	dec := findEventPayload(t, h, id, domain.EvtApprovalDecided)
	approved, ok := dec["approved"].(bool)
	if !ok || approved != false {
		t.Fatalf("decided payload approved = %T %v, want bool false", dec["approved"], dec["approved"])
	}
	if dec["approval_id"] != a.ID {
		t.Fatalf("decided payload approval_id = %v, want %s", dec["approval_id"], a.ID)
	}
	if dec["tool_name"] != a.ToolName {
		t.Fatalf("decided payload tool_name = %v, want %s", dec["tool_name"], a.ToolName)
	}
	if dec["reason"] != "nope" {
		t.Fatalf("decided payload reason = %v, want \"nope\"", dec["reason"])
	}
```

- [ ] **Step 3: 跑测试，确认 decided 断言失败**

Run: `cd backend && go test ./tests/integration/ -run 'TestApproval' -v`
Expected: FAIL — 现状 decided payload 是 `{"approved":true}`（JSON 布尔，`.(bool)` 断言本会通过），但**缺 `approval_id`/`tool_name`/`reason` 三字段**，故 `dec["approval_id"]` 为 nil ≠ `a.ID`、`dec["tool_name"]`/`dec["reason"]` 为 nil，触发 `t.Fatalf`，两测试红。

### Task 4: `run_service.go` — decided payload 落地 + 删 `boolStr`

**Files:**
- Modify: `backend/internal/service/run_service.go:1,3-14,158,166`

- [ ] **Step 1: 加 `encoding/json` import**

`run_service.go` import 块（line 3-14）当前无 `encoding/json`，加入：

```go
import (
	"context"
	"database/sql"
	"encoding/json"   // 新增
	"fmt"
	"time"
	...
)
```

- [ ] **Step 2: 改 decided payload（`run_service.go:158`）**

现状：

```go
	_, _ = s.events.Append(ctx, in.RunID, domain.EvtApprovalDecided, `{"approved":`+boolStr(in.Approve)+`}`, "normal")
```

改为（`a` 在 line 144 由 `s.approvals.Get(ctx, in.ApprovalID)` 取得，作用域内可用其 `a.ToolName`；`approved` 放 bool 保证 JSON 输出 `true`/`false`）：

```go
	approvalPayload, _ := json.Marshal(map[string]any{
		"approved":    in.Approve,
		"approval_id": in.ApprovalID,
		"tool_name":   a.ToolName,
		"reason":      in.Reason,
	})
	_, _ = s.events.Append(ctx, in.RunID, domain.EvtApprovalDecided, string(approvalPayload), "normal")
```

（变量名用 `approvalPayload` 避免与该函数内其他 `payload` 命名冲突；`reason` 可为空串，JSON 输出 `""`，前端 approved 单行不显示，符合 spec。）

- [ ] **Step 3: 删除 `boolStr`（`run_service.go:166`）**

`boolStr` 函数（全库仅 line 158 一处调用，已 grep 确认）随改造失去引用，整段删除：

```go
func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
```

- [ ] **Step 4: 跑测试，确认 decided 断言转绿**

Run: `cd backend && go test ./tests/integration/ -run 'TestApproval' -v`
Expected: PASS — decided payload 现含 `approved`(bool) + `approval_id` + `tool_name` + `reason`，Task 3 断言通过。

- [ ] **Step 5: 编译 + 全量测试 + commit**

Run: `cd backend && go build ./... && go test ./...`
```bash
git add backend/internal/service/run_service.go backend/tests/integration/approval_test.go
git commit -m "feat(approval): enrich approval.decided payload with approval_id+tool_name+reason (approved as JSON bool)"
```

### Task 5: 全量 gate（含前端零改动确认）

**Files:** none（验证 only）

- [ ] **Step 1: 全量后端测试**

Run: `cd backend && go test ./...`
Expected: 全绿（含扩展后的 `approval_test.go` + 既有 store/domain/model/tool 测试无回归）。

- [ ] **Step 2: go vet**

Run: `cd backend && go vet ./...`
Expected: 无新增告警；`boolStr` 已删无残留引用（若有 unused 告警即漏删）。

- [ ] **Step 3: 确认前端零改动**

Run: `git -C /data/home/quintin/workspace/source/go/rivus-agent diff --name-only <本轮起始SHA>..HEAD`（或 `git log --oneline` 检查）
Expected: 本轮 3 个 commit 只碰 `backend/internal/runtime/runtime.go`、`backend/internal/service/run_service.go`、`backend/tests/integration/approval_test.go`；**不含** `frontend/`。

- [ ] **Step 4: 端到端 smoke（条件性）**

如本环境有模型 endpoint（`MODEL_BASE_URL`/`MODEL_API_KEY` 已配），起服务 + 前端 dev，触发 `publish_draft` 类高风险工具 → 审批卡渲染出（含工具名 + Approve/Reject）→ 点 Approve → run 继续、卡折叠成单行。无模型 endpoint 时，集成测试（Step 1）已证明 payload 契约成立，smoke 标注"视觉受限、未执行"。

---

## 关键陷阱（执行时务必遵守）

1. **`approved` 必须是 JSON 布尔**：decided payload 用 `map[string]any` 放 `in.Approve`（bool），**不要**退回字符串拼接或 `map[string]string`。前端 `chat-messages.ts:200` 是 `p.approved === true` 严格判断，字符串 `"true"` 会被判成 rejected。测试的 `.(bool)` 类型断言专门锁死这条。
2. **requested payload 用 `map[string]string`**：`text`/`approval_id`/`tool_name` 全是字符串，`json.Marshal(map[string]string{...})` 即可，不要误用 `map[string]any`。
3. **`a.ID` 只在 `Create` 成功后可用**：handler 必须 `if err := Create(bg, a); err == nil { appendApproval(...) }`，别在 Create 前发事件（那时 `a.ID` 是空串）。
4. **保留 `FindPendingByToolCall` 幂等守卫**（`runtime.go:91`）：命中时直接 return、不重发事件（首条已持久化），双中断幂等。
5. **不要动 `runner_eino.go:277/302`** 那两条 drain 路径的 `approval.requested`（无 `approval_id`，靠前端守卫滤除）。
6. **删 `boolStr` 前确认无其他引用**（已 grep：仅 `run_service.go:158` 一处调用 + `:166` 定义）。

---

## Acceptance（本 plan 完成标准）

- `approval.requested` payload 含 `text` + `approval_id`(=该审批记录 ID) + `tool_name`；仅在 `Approvals.Create` 成功后发射。
- `approval.decided` payload 含 `approved`(JSON bool) + `approval_id` + `tool_name` + `reason`。
- `boolStr` 已删除、无残留引用。
- `cd backend && go test ./...` 全绿，`go vet ./...` 无新增告警。
- 前端零改动（`git diff` 不含 `frontend/`）；集成测试证明 payload 契约，前端既有 `parseApprovalPayload`/`ApprovalCard.parseApproval` 因此无需改动即可复活 pending 审批卡。
