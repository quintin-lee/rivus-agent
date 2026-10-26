# Rivus Agent 后端优化 A–E 实现计划

> **For agentic workers:** REQUIRED: Use superpowers:executing-plans to implement this plan (user constraint: 本项目不使用 subagent). Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 按已评审 spec（`docs/superpowers/specs/2026-10-26-backend-optimization-design.md`，commit 9621cbf）实现五项后端优化：预算强制执行、幂等缓存修复、Resume 预算继承、SQLite 连接池、事件丢失与可观测性。

**Architecture:** 全部为现有模块内最小改动 —— `runtime` 包内让 `BudgetTracker` 真正生效（事件循环 post-gate + Cancel），`tool` 包修 defer bug 并加输出截断，`store` 包放开 WAL 并发读，`observability` 包加 expvar 桶；不改 HTTP/JSON 公开 API、不改 DB schema、不引新依赖。

**Tech Stack:** Go 1.27 / modernc.org/sqlite (WAL) / CloudWeGo Eino ADK / expvar；测试 `go test ./...` + 现有 `tests/integration` 探针 harness。

**计划文件保存位置:** `docs/superpowers/plans/2026-10-26-backend-optimization-plan.md`（沿用仓库既有 plan 的目录约定；偏离 todo 中的 `.omo/plans/`）。

---

## Chunk 1: 修复 B（幂等缓存）+ A-1（预算跟踪器）

### Task 1: 修复幂等工具缓存被 defer 清空（方向 B）

**Files:**
- Create: `backend/internal/tool/executor_test.go`
- Modify: `backend/internal/tool/executor.go:64-68`

- [ ] **Step 1.1: 写失败测试**

新建 `backend/internal/tool/executor_test.go`：

```go
package tool

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
)

// registerTestTool 注册一个只读、永不需要审批的测试工具。
func registerTestTool(t *testing.T, idempotent bool, handler func(ctx context.Context, args json.RawMessage) (string, error)) *Executor {
	t.Helper()
	reg := NewRegistry()
	if err := reg.Register(Definition{
		Name:           "echo",
		Description:    "echo test tool",
		Version:        "1.0.0",
		InputSchema:    `{"type":"object"}`,
		Risk:           RiskRead,
		Approval:       ApprovalNever,
		Idempotent:     idempotent,
		TimeoutMs:      1000,
		MaxResultBytes: 1024,
		Handler:        handler,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	return NewExecutor(reg)
}

// 幂等工具同 key 重复调用必须返回首次真实结果（不是空串），且 handler 只执行一次。
func TestIdempotentCachesRealResult(t *testing.T) {
	var calls atomic.Int32
	ex := registerTestTool(t, true, func(ctx context.Context, args json.RawMessage) (string, error) {
		calls.Add(1)
		return "real-output", nil
	})
	ctx := context.Background()
	req := ExecRequest{RunID: "r1", ToolCallID: "tc1", ToolName: "echo", Args: json.RawMessage(`{}`)}

	first, err := ex.Execute(ctx, req)
	if err != nil {
		t.Fatalf("first execute: %v", err)
	}
	second, err := ex.Execute(ctx, req)
	if err != nil {
		t.Fatalf("second execute: %v", err)
	}
	if first.Output != "real-output" || second.Output != "real-output" {
		t.Fatalf("outputs = %q, %q; want both %q", first.Output, second.Output, "real-output")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("handler called %d times; want 1", got)
	}
}

// 幂等工具失败不入缓存：重试必须真的重新执行。
func TestIdempotentFailureNotCached(t *testing.T) {
	var calls atomic.Int32
	ex := registerTestTool(t, true, func(ctx context.Context, args json.RawMessage) (string, error) {
		if calls.Add(1) == 1 {
			return "", errors.New("boom")
		}
		return "recovered", nil
	})
	ctx := context.Background()
	req := ExecRequest{RunID: "r1", ToolCallID: "tc1", ToolName: "echo", Args: json.RawMessage(`{}`)}

	if _, err := ex.Execute(ctx, req); err == nil {
		t.Fatal("first execute should fail")
	}
	res, err := ex.Execute(ctx, req)
	if err != nil {
		t.Fatalf("retry execute: %v", err)
	}
	if res.Output != "recovered" {
		t.Fatalf("retry output = %q; want %q", res.Output, "recovered")
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("handler called %d times; want 2", got)
	}
}
```

- [ ] **Step 1.2: 运行确认失败**

Run: `cd backend && go test ./internal/tool/ -run 'TestIdempotent' -v`
Expected: **FAIL** — `TestIdempotentCachesRealResult` 第二次调用输出 `""`（defer 把缓存清成空串）；`TestIdempotentFailureNotCached` 第二次调用返回 nil error + 空输出。

- [ ] **Step 1.3: 最小修复 — 删除 defer 块**

`backend/internal/tool/executor.go` 删除 L64-68（`if def.Idempotent { ... }` 内的 defer），该块变为：

```go
	if def.Idempotent {
		key := req.RunID + ":" + ToolCallID
		e.mu.Lock()
		if prev, dup := e.seen[key]; dup {
			e.mu.Unlock()
			return &ExecResult{Output: prev, OutputSize: len(prev)}, nil
		}
		e.mu.Unlock()
	}
```

（即仅移除 `defer func(){ e.mu.Lock(); e.seen[key] = ""; e.mu.Unlock() }()`。成功路径 L88-93 的 `e.seen[key] = out` 保留；失败路径本就不写缓存。）

- [ ] **Step 1.4: 运行确认通过**

Run: `cd backend && go test ./internal/tool/ -v`
Expected: **PASS**（含既有 policy/registry 测试）。

- [ ] **Step 1.5: Commit**

```bash
git add backend/internal/tool/executor.go backend/internal/tool/executor_test.go
git commit -m "fix(tool): idempotent cache was cleared by defer, repeated calls returned empty output"
```

---

### Task 2: EvtBudgetExceeded + BudgetTracker.OverBudget（方向 A-1）

**Files:**
- Modify: `backend/internal/domain/event.go:20`（EvtBudgetConsumed 之后加一行）
- Modify: `backend/internal/runtime/budget.go`（文件末尾加方法）
- Create: `backend/internal/runtime/budget_test.go`

- [ ] **Step 2.1: 写失败测试**

新建 `backend/internal/runtime/budget_test.go`：

```go
package runtime

import (
	"testing"

	"rivus-agent-backend/internal/domain"
)

func TestOverBudgetZeroLimitsNeverTrip(t *testing.T) {
	tr := NewBudgetTracker(domain.Budget{})
	tr.AddModelCall()
	tr.AddToolCall()
	tr.AddOutput(1 << 20)
	if code, hit := tr.OverBudget(); hit {
		t.Fatalf("zero limits hit with code %q; want no hit", code)
	}
}

func TestOverBudgetDimensions(t *testing.T) {
	tests := []struct {
		name     string
		limit    domain.Budget
		use      func(tr *BudgetTracker)
		wantHit  bool
		wantCode string
	}{
		{"tool below", domain.Budget{MaxToolCalls: 2}, func(tr *BudgetTracker) { tr.AddToolCall() }, false, ""},
		{"tool at", domain.Budget{MaxToolCalls: 2}, func(tr *BudgetTracker) { tr.AddToolCall(); tr.AddToolCall() }, true, "tool_calls_budget_exceeded"},
		{"model below", domain.Budget{MaxModelCalls: 2}, func(tr *BudgetTracker) { tr.AddModelCall() }, false, ""},
		{"model at", domain.Budget{MaxModelCalls: 1}, func(tr *BudgetTracker) { tr.AddModelCall() }, true, "model_calls_budget_exceeded"},
		{"output below", domain.Budget{MaxOutputBytes: 10}, func(tr *BudgetTracker) { tr.AddOutput(9) }, false, ""},
		{"output at", domain.Budget{MaxOutputBytes: 10}, func(tr *BudgetTracker) { tr.AddOutput(10) }, true, "output_bytes_budget_exceeded"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := NewBudgetTracker(tt.limit)
			tt.use(tr)
			code, hit := tr.OverBudget()
			if hit != tt.wantHit || code != tt.wantCode {
				t.Fatalf("OverBudget() = (%q, %v); want (%q, %v)", code, hit, tt.wantCode, tt.wantHit)
			}
		})
	}
}

// 触限优先级固定：tool > model > output。
func TestOverBudgetPriorityToolFirst(t *testing.T) {
	tr := NewBudgetTracker(domain.Budget{MaxToolCalls: 1, MaxModelCalls: 1, MaxOutputBytes: 1})
	tr.AddToolCall()
	tr.AddModelCall()
	tr.AddOutput(1)
	code, hit := tr.OverBudget()
	if !hit || code != "tool_calls_budget_exceeded" {
		t.Fatalf("OverBudget() = (%q, %v); want tool_calls_budget_exceeded", code, hit)
	}
}
```

- [ ] **Step 2.2: 运行确认失败**

Run: `cd backend && go test ./internal/runtime/ -run 'TestOverBudget' -v`
Expected: **编译失败** — `tr.OverBudget undefined`。

- [ ] **Step 2.3: 实现**

`backend/internal/domain/event.go` 在 L20 `EvtBudgetConsumed` 行后加：

```go
	EvtBudgetExceeded    EventType = "budget.exceeded" // run 终态化于预算触限
```

`backend/internal/runtime/budget.go` 文件末尾加：

```go
// OverBudget 返回首个触限的预算维度错误码。检查顺序固定 tool > model > output；
// limit<=0 的维度视为不限。usage >= limit 即触限（post-gate 语义，见 spec §A）。
func (b *BudgetTracker) OverBudget() (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit.MaxToolCalls > 0 && b.usage.ToolCalls >= b.limit.MaxToolCalls {
		return "tool_calls_budget_exceeded", true
	}
	if b.limit.MaxModelCalls > 0 && b.usage.ModelCalls >= b.limit.MaxModelCalls {
		return "model_calls_budget_exceeded", true
	}
	if b.limit.MaxOutputBytes > 0 && b.usage.OutputBytes >= b.limit.MaxOutputBytes {
		return "output_bytes_budget_exceeded", true
	}
	return "", false
}
```

- [ ] **Step 2.4: 运行确认通过**

Run: `cd backend && go test ./internal/runtime/ -run 'TestOverBudget' -v && cd .. && go build ./...`
Expected: **PASS** + build OK。

- [ ] **Step 2.5: Commit**

```bash
git add backend/internal/domain/event.go backend/internal/runtime/budget.go backend/internal/runtime/budget_test.go
git commit -m "feat(budget): add EvtBudgetExceeded and BudgetTracker.OverBudget post-gate"
```

---

## Chunk 2: 预算接线 + 输出截断 + Resume 预算

### Task 3: runtime 事件循环预算接线 + 集成测试（方向 A-2 + A-4）

**Files:**
- Create: `backend/tests/integration/budget_cap_test.go`
- Modify: `backend/internal/runtime/runtime.go:64-65, 120-171, 198-205`

- [ ] **Step 3.1: 写失败集成测试**

新建 `backend/tests/integration/budget_cap_test.go`（package `integration`；事件读取方式与 `budget_consumed_test.go` 一致 —— 若 `ListAfter` 元素类型/字段访问不同，以该文件为准微调）：

```go
package integration

import (
	"context"
	"testing"
	"time"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/tests/testutil"
)

func budgetPayload(t *testing.T, ses string, overrides map[string]any) map[string]any {
	t.Helper()
	payload := fixturePayload(t, ses)
	b, ok := payload["budget"].(map[string]any)
	if !ok {
		t.Fatalf("fixture payload budget missing or wrong type: %#v", payload["budget"])
	}
	for k, v := range overrides {
		b[k] = v
	}
	return payload
}

func eventTypes(t *testing.T, h *testutil.Harness, runID string) []string {
	t.Helper()
	evs := h.Events.ListAfter(context.Background(), runID, 0, 500)
	out := make([]string, 0, len(evs))
	for _, ev := range evs {
		out = append(out, string(ev.Type))
	}
	return out
}

// 工具预算触顶：run 必须终态 RunFailed + error_code=tool_calls_budget_exceeded + budget.exceeded 事件。
func TestBudgetCapToolCalls(t *testing.T) {
	h := testutil.New(t)
	h.Model.EnqueueToolCall("tc1", "knowledge_search", `{"query":"q1","top_k":3}`)
	h.Model.EnqueueText("最终答案")
	ses := h.CreateSession("s")

	payload := budgetPayload(t, ses, map[string]any{"max_tool_calls": 1})
	id, code, _ := h.CreateRun(ses, payload, "bc-tool-1")
	if code != 201 {
		t.Fatalf("CreateRun code = %d, want 201", code)
	}
	run := h.WaitTerminal(id, 30*time.Second)
	if run.Status != domain.RunFailed {
		t.Fatalf("status = %s, want failed (events=%v)", run.Status, eventTypes(t, h, id))
	}
	if run.ErrorCode != "tool_calls_budget_exceeded" {
		t.Fatalf("error_code = %q, want tool_calls_budget_exceeded", run.ErrorCode)
	}
	if !containsType(eventTypes(t, h, id), string(domain.EvtBudgetExceeded)) {
		t.Fatalf("missing budget.exceeded event; events = %v", eventTypes(t, h, id))
	}
}

// 模型预算触顶：第一次 model.completed 即触限。
func TestBudgetCapModelCalls(t *testing.T) {
	h := testutil.New(t)
	h.Model.EnqueueText("第一轮回答")
	ses := h.CreateSession("s")

	payload := budgetPayload(t, ses, map[string]any{"max_model_calls": 1})
	id, code, _ := h.CreateRun(ses, payload, "bc-model-1")
	if code != 201 {
		t.Fatalf("CreateRun code = %d, want 201", code)
	}
	run := h.WaitTerminal(id, 30*time.Second)
	if run.Status != domain.RunFailed || run.ErrorCode != "model_calls_budget_exceeded" {
		t.Fatalf("status=%s error_code=%q; want failed/model_calls_budget_exceeded (events=%v)",
			run.Status, run.ErrorCode, eventTypes(t, h, id))
	}
	if !containsType(eventTypes(t, h, id), string(domain.EvtBudgetExceeded)) {
		t.Fatalf("missing budget.exceeded event; events = %v", eventTypes(t, h, id))
	}
}

// 对照组：宽松预算下不得出现 budget.exceeded（防误杀）。
func TestBudgetNoFalsePositive(t *testing.T) {
	h := testutil.New(t)
	h.Model.EnqueueToolCall("tc1", "knowledge_search", `{"query":"q1","top_k":3}`)
	h.Model.EnqueueText("最终答案")
	ses := h.CreateSession("s")

	payload := budgetPayload(t, ses, map[string]any{"max_tool_calls": 10, "max_model_calls": 10})
	id, code, _ := h.CreateRun(ses, payload, "bc-ctl-1")
	if code != 201 {
		t.Fatalf("CreateRun code = %d, want 201", code)
	}
	_ = h.WaitTerminal(id, 30*time.Second)
	if containsType(eventTypes(t, h, id), string(domain.EvtBudgetExceeded)) {
		t.Fatalf("budget.exceeded fired under loose budget; events = %v", eventTypes(t, h, id))
	}
}

func containsType(types []string, want string) bool {
	for _, s := range types {
		if s == want {
			return true
		}
	}
	return false
}
```

**断言策略说明**：spec 提到 probe-pin 事件计数；本计划改为**存在性断言**（status + error_code + budget.exceeded 存在/不存在），因为触限后 Cancel 与 drain 收尾存在竞速，事件总数不确定，而终态与 error_code 由 budgetCode 保证确定（budgetCode 在处理触限事件时已置位，排在 run.finished 之前）。更稳且不弱于 spec 意图。

- [ ] **Step 3.2: 运行确认失败**

Run: `cd backend && go test ./tests/integration/ -run 'TestBudgetCap|TestBudgetNoFalse' -v -timeout 120s`
Expected: **FAIL** — 当前无强制执行：`TestBudgetCapToolCalls` 得到非 budget 的 error_code（或成功/verification_failed），且事件里没有 `budget.exceeded`。

- [ ] **Step 3.3: 实现 runtime.go 接线**

`backend/internal/runtime/runtime.go` 五处修改：

**(a) L64-65** 删掉 `_ = tracker`：

```go
	tracker := NewBudgetTracker(budget)
```

**(b) L120-125** 局部计数器改为预算状态：

```go
	started := time.Now()
	finalText := ""
	waitApproval := false
	modelFailed := false
	budgetFired := false
	budgetCode := ""
```

（删除 `toolEvidence := 0` 与 `modelCalls := 0` 两行。）

**(c) 事件循环 L126-142** 整体替换为：

```go
	for ev := range ch {
		switch ev.Type {
		case domain.EvtToolCompleted:
			tracker.AddToolCall()
			r.appendBudget(ctx, run.ID, tracker, started)
		case domain.EvtApprovalRequested:
			waitApproval = true
		case domain.EvtModelCompleted:
			finalText = ev.Text
			tracker.AddModelCall()
			tracker.AddOutput(len(ev.Text))
			r.appendBudget(ctx, run.ID, tracker, started)
		case domain.EvtModelFailed:
			modelFailed = true
		case domain.EvtRunFailed:
			finalText = ev.Text
		}
		if code, hit := tracker.OverBudget(); hit && !budgetFired {
			budgetFired = true
			budgetCode = code
			_ = r.deps.Runner.Cancel(run.ID)
		}
	}
	u := tracker.Snapshot()
```

**(d) 循环后分支重排** —— waitApproval 分支保持原样；其**之后**、DeadlineExceeded 分支**之前**插入预算终态分支；随后所有 `toolEvidence` 换成 `u.ToolCalls`；Verify 前加输出截断：

```go
	if waitApproval || needApproval.Load() {
		_ = r.deps.Tasks.UpdateStatus(ctx, ownerID, run.ID, domain.RunWaitingApproval, "", "")
		observability.IncrRunFinished(false)
		return
	}
	if budgetCode != "" {
		// Cancel 已让 runner 退出；沿用 bgCtx 模式（ctx 可能已随 cancel 失效）。
		bgCtx := context.Background()
		msg := "budget exceeded: " + budgetCode
		_ = r.deps.Tasks.UpdateStatus(bgCtx, ownerID, run.ID, domain.RunFailed, budgetCode, msg)
		r.append(bgCtx, run.ID, domain.EvtBudgetExceeded, msg)
		observability.IncrRunFinished(false)
		r.deps.Runner.DeleteCheckpointsByRun(bgCtx, run.ID)
		return
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		// （原分支原样保留，含 bgCtx 注释与 RunTimedOut 逻辑）
	}
	if modelFailed && u.ToolCalls == 0 {
		// （原分支，仅 toolEvidence → u.ToolCalls）
	}
	if budget.MaxOutputBytes > 0 && len(finalText) > budget.MaxOutputBytes {
		finalText = finalText[:budget.MaxOutputBytes] + "...[truncated]"
	}
	report := Verify(run.SuccessCriteria, finalText, u.ToolCalls)
	if report.Passed && len(run.SuccessCriteria) == 0 && u.ToolCalls == 0 && finalText == "" {
		// （原 guard 不变）
	}
```

（其余 pass/fail 分支不动。）

**(e) appendBudget 签名 L198-205 改为读 tracker 快照**（payload key/语义逐字节等价，保住 `TestBudgetConsumed`）：

```go
func (r *Runtime) appendBudget(ctx context.Context, runID string, tracker *BudgetTracker, started time.Time) {
	u := tracker.Snapshot()
	payload, _ := json.Marshal(map[string]int{
		"model_calls":      u.ModelCalls,
		"tool_calls":       u.ToolCalls,
		"duration_seconds": int(time.Since(started).Seconds()),
	})
	_, _ = r.deps.Events.Append(ctx, runID, domain.EvtBudgetConsumed, string(payload), "normal")
}
```

（注：append/appendBudget 此处保留 `_, _ =`，错误日志在 Task 9 统一加。）

- [ ] **Step 3.4: 运行确认通过 + 回归**

Run: `cd backend && go test ./internal/runtime/ ./tests/integration/ -run 'TestBudget|TestOverBudget' -v -timeout 180s`
Expected: 新增 3 个集成测试 **PASS**，`TestBudgetConsumed` 回归 **PASS**。

- [ ] **Step 3.5: 全量集成回归**

Run: `cd backend && go test ./tests/integration/ -timeout 300s`
Expected: 全绿。若有既有测试因触限失败 → 检查该测试预算是否确实过紧（默认 fixture 10/10/6/50000 应足够），不得放宽断言来迁就。

- [ ] **Step 3.6: Commit**

```bash
git add backend/internal/runtime/runtime.go backend/tests/integration/budget_cap_test.go
git commit -m "feat(runtime): enforce budget in event loop, terminate runs with budget.exceeded"
```

---

### Task 4: 全局输出字节截断（方向 A-3）

**Files:**
- Modify: `backend/internal/tool/executor.go:16-24`（ExecRequest 加字段）与 `:84-86` 后（截断逻辑）
- Modify: `backend/internal/runtime/runner_eino.go:146-152`（gatewayTool 加字段）、`:167-169`、`:175-177`（两处 Execute 传参）
- Modify: `backend/internal/runtime/runner_child.go:93-94`（构造处传 budget.MaxOutputBytes）
- Test: `backend/internal/tool/executor_test.go`（追加）

- [ ] **Step 4.1: 写失败测试**

`executor_test.go` 追加：

```go
// 全局输出预算（req.MaxOutputBytes）在 per-tool 截断之后生效。
func TestExecuteGlobalOutputTruncation(t *testing.T) {
	ex := registerTestTool(t, false, func(ctx context.Context, args json.RawMessage) (string, error) {
		return "0123456789abcdefghij", nil // 20 bytes
	})
	ctx := context.Background()
	res, err := ex.Execute(ctx, ExecRequest{
		RunID: "r1", ToolCallID: "tc1", ToolName: "echo",
		Args: json.RawMessage(`{}`), MaxOutputBytes: 10,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	want := "0123456789...[truncated]"
	if res.Output != want {
		t.Fatalf("output = %q; want %q", res.Output, want)
	}
}
```

- [ ] **Step 4.2: 运行确认失败**

Run: `cd backend && go test ./internal/tool/ -run 'TestExecuteGlobalOutputTruncation' -v`
Expected: **编译失败** — `ExecRequest.MaxOutputBytes` undefined。

- [ ] **Step 4.3: 实现**

1. `ExecRequest` 加字段：`MaxOutputBytes int`（放 `Approved` 后）。
2. `Execute` 中 L84-86 per-tool 截断之后、L87 `ObserveTool` 之前插入：

```go
	if req.MaxOutputBytes > 0 && len(out) > req.MaxOutputBytes {
		out = out[:req.MaxOutputBytes] + "...[truncated]"
	}
```

（截断发生在写入 `e.seen` 之前 → 幂等缓存存截断后的值，一致。）

3. `gatewayTool` struct 加字段 `maxOutputBytes int`。
4. `InvokableRun` 两处 `ExecRequest` 字面量各加一行：`MaxOutputBytes: g.maxOutputBytes,`。
5. `runner_child.go` L93-94 构造处加：`maxOutputBytes: budget.MaxOutputBytes,`（buildAgentFiltered 唯一构造点，父/子 agent 同时覆盖）。

- [ ] **Step 4.4: 运行确认通过**

Run: `cd backend && go test ./internal/tool/ ./internal/runtime/ -v -timeout 120s`
Expected: 新测试 **PASS**，既有 runtime 测试全绿（gatewayTool 改动不破坏 runner_child_test / runner_stream_test / runner_failed_tool_test）。

- [ ] **Step 4.5: Commit**

```bash
git add backend/internal/tool/executor.go backend/internal/tool/executor_test.go backend/internal/runtime/runner_eino.go backend/internal/runtime/runner_child.go
git commit -m "feat(tool): enforce MaxOutputBytes on tool outputs via gateway"
```

---

### Task 5: Resume 预算继承（方向 C）

**Files:**
- Modify: `backend/internal/runtime/runner_eino.go:45-49`（ResumeRequest 加字段）、`:230`（executeResume 用解析函数）
- Create: `backend/internal/runtime/runner_eino_test.go`

- [ ] **Step 5.1: 写失败测试**

新建 `backend/internal/runtime/runner_eino_test.go`：

```go
package runtime

import (
	"testing"

	"rivus-agent-backend/internal/domain"
)

// ResumeRequest 未带预算（全零）→ 回退服务端默认上限（原硬编码值）。
func TestResolveResumeBudgetDefaults(t *testing.T) {
	got := resolveResumeBudget(domain.Budget{})
	want := domain.Budget{MaxIterations: 12, MaxModelCalls: 30, MaxToolCalls: 50, MaxDurationSeconds: 600}
	if got != want {
		t.Fatalf("resolveResumeBudget(zero) = %+v; want %+v", got, want)
	}
}

// 带预算则原样透传（调用方已 ClampByServer，只收紧不放宽）。
func TestResolveResumeBudgetPassthrough(t *testing.T) {
	in := domain.Budget{MaxIterations: 5, MaxModelCalls: 7, MaxToolCalls: 3, MaxDurationSeconds: 90, MaxOutputBytes: 1234}
	if got := resolveResumeBudget(in); got != in {
		t.Fatalf("resolveResumeBudget(%+v) = %+v; want passthrough", in, got)
	}
}
```

- [ ] **Step 5.2: 运行确认失败**

Run: `cd backend && go test ./internal/runtime/ -run 'TestResolveResumeBudget' -v`
Expected: **编译失败** — `resolveResumeBudget undefined`。

- [ ] **Step 5.3: 实现**

`runner_eino.go`：

1. `ResumeRequest` 加字段：

```go
type ResumeRequest struct {
	RunID         string
	CheckpointID  string
	ApprovedTools map[string]bool
	Budget        domain.Budget
}
```

2. 文件内加纯函数（放在 ResumeRequest 附近）：

```go
// resolveResumeBudget：Resume 未携带预算（全零）时退回服务端默认上限；
// 携带则原样使用（RunService 创建时已 ClampByServer，只收紧不放宽）。
func resolveResumeBudget(b domain.Budget) domain.Budget {
	if b == (domain.Budget{}) {
		return domain.Budget{MaxIterations: 12, MaxModelCalls: 30, MaxToolCalls: 50, MaxDurationSeconds: 600}
	}
	return b
}
```

3. `executeResume` L230 改为：

```go
	agent, err := r.buildAgent(ctx, req.RunID, resolveResumeBudget(req.Budget), req.ApprovedTools)
```

**边界**（spec §C）：`run_service.go` 不改 —— 现有唯一调用链（HTTP→RunService.Resume→executeNew）已正确传 `run.Budget`；本任务只消除 executeResume 的硬编码，不发明新调用方（YAGNI）。

- [ ] **Step 5.4: 运行确认通过**

Run: `cd backend && go test ./internal/runtime/ -v -timeout 120s`
Expected: 新 2 测试 **PASS**，runtime 包全绿。

- [ ] **Step 5.5: Commit**

```bash
git add backend/internal/runtime/runner_eino.go backend/internal/runtime/runner_eino_test.go
git commit -m "feat(runtime): resume inherits request budget instead of hardcoded caps"
```

---

## Chunk 3: SQLite 并发 + 可观测性 + 全量验证

### Task 6: SQLite 文件库连接池（方向 D）

**Files:**
- Modify: `backend/internal/store/sqlite.go:3-11`（import strings）、`:22-23`（连接数）、`:54-55`、`:74`（过期注释）
- Create: `backend/tests/integration/sqlite_concurrency_test.go`

- [ ] **Step 6.1: 写失败测试**

新建 `backend/tests/integration/sqlite_concurrency_test.go`：

```go
package integration

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/store"
)

// 文件库在仍有未提交事务占用连接时，其他连接的写入不得被连接池饿死。
// 旧实现 MaxOpenConns(1) 下该 goroutine 会永久阻塞（池中无空闲连接）。
func TestSQLiteWriteNotBlockedByOpenTx(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "concurrent.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	// 持有一个只读事务：占用一条连接，但不持有写锁。
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.QueryContext(context.Background(), "SELECT 1"); err != nil {
		t.Fatalf("tx read: %v", err)
	}

	events := store.NewEventRepo(db)
	done := make(chan error, 1)
	go func() {
		_, err := events.Append(context.Background(), "conc-run-1", domain.EvtRunCreated, `{}`, "normal")
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("append: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Append blocked >2s while a read tx held the only connection — SetMaxOpenConns still 1?")
	}
}

// 8 协程并发 CreateRun + Append + GetRun 全部成功且可读。
func TestSQLiteConcurrentWorkers(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "workers.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	tasks := store.NewTaskRepo(db)
	events := store.NewEventRepo(db)

	const workers = 8
	var wg sync.WaitGroup
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx := context.Background()
			runID := fmt.Sprintf("conc-run-%d", i)
			run := &domain.Run{
				ID: runID, SessionID: "s", OwnerID: "o", Goal: "g", Mode: "react",
				Status: domain.RunQueued, IdempotencyKey: fmt.Sprintf("idem-%d", i),
			}
			if err := tasks.CreateRun(ctx, run); err != nil {
				errs[i] = fmt.Errorf("create: %w", err)
				return
			}
			if _, err := events.Append(ctx, runID, domain.EvtRunCreated, `{}`, "normal"); err != nil {
				errs[i] = fmt.Errorf("append: %w", err)
				return
			}
			if _, err := tasks.GetRun(ctx, "o", runID); err != nil {
				errs[i] = fmt.Errorf("get: %w", err)
			}
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", i, err)
		}
	}
}
```

**注**：`domain.Run` 的 NOT NULL/唯一列以 `task_repo.go CreateRun` 的 INSERT 与 `schema.sql` 为准；若编译/运行报缺字段，按需补齐（`IdempotencyKey` 已预置唯一值防 UNIQUE 冲突）。

- [ ] **Step 6.2: 运行确认失败**

Run: `cd backend && go test ./tests/integration/ -run 'TestSQLite' -v -timeout 60s`
Expected: `TestSQLiteWriteNotBlockedByOpenTx` **FAIL**（`Append blocked >2s...`）；`TestSQLiteConcurrentWorkers` 串行但可通过（预期）。

- [ ] **Step 6.3: 实现**

`backend/internal/store/sqlite.go`：

1. import 加 `"strings"`。
2. L22-23 替换为：

```go
	// 文件库放开 WAL 并发（读写走不同连接）；内存库必须单连接 ——
	// SQLite :memory: 是 per-connection 的，多连接会各持独立空库（test DB 被分片）。
	conns := 8
	if path == ":memory:" || strings.Contains(path, "mode=memory") {
		conns = 1
	}
	db.SetMaxOpenConns(conns)
	db.SetMaxIdleConns(conns)
```

3. L54-55 注释更新为：`// 注意：经 tx 查（与迁移同事务，读到的是本事务视图；单连接时代的跨连接死锁已随多连接消除）。`
4. L74 注释更新为：`// colExists 用 PRAGMA table_info 查列是否存在（经 tx 查，复用迁移事务视图）。`

**5 个 Open 调用方零改动**（app.go:50 文件路径→8；harness/bench/factory 的 `:memory:`→仍 1）。既有 pragma（WAL/busy_timeout=5000/foreign_keys/synchronous=NORMAL）不动。

- [ ] **Step 6.4: 运行确认通过**

Run: `cd backend && go test ./tests/integration/ -run 'TestSQLite' -v -timeout 60s && cd backend && go test ./... -timeout 300s`
Expected: 两测试 **PASS**；全量套件 **PASS**（重点观察 `:memory:` 相关测试仍走单连接、无 DB 分片）。

- [ ] **Step 6.5: Commit**

```bash
git add backend/internal/store/sqlite.go backend/tests/integration/sqlite_concurrency_test.go
git commit -m "perf(store): 8 conns for file DB under WAL, keep single conn for :memory:"
```

---

### Task 7: expvar 延迟桶 + 事件失败计数（方向 E-1）

**Files:**
- Modify: `backend/internal/observability/metrics.go`
- Create: `backend/internal/observability/metrics_test.go`

- [ ] **Step 7.1: 写失败测试**

新建 `backend/internal/observability/metrics_test.go`：

```go
package observability

import (
	"expvar"
	"testing"
)

var bucketNames = []string{
	"latency_ms_0_50", "latency_ms_50_100", "latency_ms_100_200",
	"latency_ms_200_500", "latency_ms_500_plus",
}

func bucketValues(t *testing.T) []int64 {
	t.Helper()
	out := make([]int64, len(bucketNames))
	for i, n := range bucketNames {
		v, ok := expvar.Get(n).(*expvar.Int)
		if !ok {
			t.Fatalf("expvar %q missing", n)
		}
		out[i] = v.Value()
	}
	return out
}

func TestObserveLatencyMsBuckets(t *testing.T) {
	tests := []struct {
		ms    int64
		index int
	}{
		{0, 0}, {49, 0}, {50, 1}, {99, 1}, {100, 2}, {199, 2},
		{200, 3}, {499, 3}, {500, 4}, {1 << 40, 4},
	}
	for _, tt := range tests {
		before := bucketValues(t)
		ObserveLatencyMs(tt.ms)
		after := bucketValues(t)
		for i := range after {
			want := int64(0)
			if i == tt.index {
				want = 1
			}
			if after[i]-before[i] != want {
				t.Fatalf("ms=%d: bucket %d delta=%d, want %d (all=%v)", tt.ms, i, after[i]-before[i], want, after)
			}
		}
	}
}

// ObserveTool 透传到桶。
func TestObserveToolFeedsLatencyBucket(t *testing.T) {
	before := bucketValues(t)
	ObserveTool("probe_tool", true, 75)
	after := bucketValues(t)
	if after[1]-before[1] != 1 {
		t.Fatalf("bucket_50_100 delta=%d, want 1", after[1]-before[1])
	}
}

func TestIncrEventAppendFailed(t *testing.T) {
	v, ok := expvar.Get("event_append_failed").(*expvar.Int)
	if !ok {
		t.Fatal("expvar event_append_failed missing")
	}
	before := v.Value()
	IncrEventAppendFailed()
	if got := v.Value(); got != before+1 {
		t.Fatalf("event_append_failed=%d, want %d", got, before+1)
	}
}
```

- [ ] **Step 7.2: 运行确认失败**

Run: `cd backend && go test ./internal/observability/ -v`
Expected: **编译失败** — `ObserveLatencyMs` / `IncrEventAppendFailed` / 桶 expvar 未定义。

- [ ] **Step 7.3: 实现**

`metrics.go`：

1. var 块加：

```go
	eventAppendFailed = expvar.NewInt("event_append_failed")

	latencyBuckets = []*expvar.Int{
		expvar.NewInt("latency_ms_0_50"),
		expvar.NewInt("latency_ms_50_100"),
		expvar.NewInt("latency_ms_100_200"),
		expvar.NewInt("latency_ms_200_500"),
		expvar.NewInt("latency_ms_500_plus"),
	}
```

2. 加两个函数；`ObserveTool` 在 `mu.Unlock()` 之后尾部调用 `ObserveLatencyMs(latencyMs)`：

```go
// IncrEventAppendFailed 记录事件落库失败（配合 runtime 侧 slog Warn）。
func IncrEventAppendFailed() { eventAppendFailed.Add(1) }

// ObserveLatencyMs 把时延计入 5 个桶（50/100/200/500/∞ ms），
// 外部抓取 expvar 后可算直方图分位数。
func ObserveLatencyMs(ms int64) {
	switch {
	case ms < 50:
		latencyBuckets[0].Add(1)
	case ms < 100:
		latencyBuckets[1].Add(1)
	case ms < 200:
		latencyBuckets[2].Add(1)
	case ms < 500:
		latencyBuckets[3].Add(1)
	default:
		latencyBuckets[4].Add(1)
	}
}
```

3. **不动** `latSumMs/latCount`（死代码，spec 明示留置）。

- [ ] **Step 7.4: 运行确认通过**

Run: `cd backend && go test ./internal/observability/ -v`
Expected: 3 测试 **PASS**（delta 断言对 `-count` 重跑免疫）。

- [ ] **Step 7.5: Commit**

```bash
git add backend/internal/observability/metrics.go backend/internal/observability/metrics_test.go
git commit -m "feat(observability): expvar latency histogram buckets + event_append_failed counter"
```

---

### Task 8: emit 阻塞式发送（方向 E-2）

**Files:**
- Modify: `backend/internal/runtime/runner_eino.go:341-350`（emit 本体）+ 13 处调用点
- Modify: `backend/internal/runtime/runner_child.go:122`（1 处调用点）
- Test: `backend/internal/runtime/runner_eino_test.go`（追加）

- [ ] **Step 8.1: 写失败测试**

`runner_eino_test.go` 追加：

```go
import (
	"context"
	"time"

	"rivus-agent-backend/internal/domain"
)

// 缓冲满且无消费者时 emit 阻塞（事件不被静默丢弃）；ctx 取消后放弃。
func TestEmitBlocksWhenFull(t *testing.T) {
	r := NewEinoRunner(nil, nil, nil, nil)
	out := make(chan RuntimeEvent) // 无缓冲，无读者
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.emit(ctx, out, "run-1", domain.EvtRunStarted, "started", "", "")
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("emit returned while consumer absent (event dropped)")
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("emit did not return after ctx cancel")
	}
}

// 缓冲有空位时即使 ctx 已取消也必须投递 —— 保证 drain 的 run.cancelled 不被随机丢弃。
func TestEmitDeliversWhenRoomDespiteCancel(t *testing.T) {
	r := NewEinoRunner(nil, nil, nil, nil)
	out := make(chan RuntimeEvent, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.emit(ctx, out, "run-1", domain.EvtRunCancelled, "cancelled", "", "")
	select {
	case ev := <-out:
		if ev.Type != domain.EvtRunCancelled {
			t.Fatalf("ev.Type=%s, want run.cancelled", ev.Type)
		}
	default:
		t.Fatal("event dropped although buffer had room")
	}
}
```

（若 import 已存在则合并；`runner_eino_test.go` 的 import 块最终为 `context`/`testing`/`time`/`domain`。）

- [ ] **Step 8.2: 运行确认失败**

Run: `cd backend && go test ./internal/runtime/ -run 'TestEmit' -v`
Expected: **编译失败** — `r.emit` 无 ctx 参数（调用点不匹配）。

- [ ] **Step 8.3: 实现**

`runner_eino.go` emit 替换为**两段式**（先尽力非阻塞投递，满了才阻塞并以 ctx 为逃生门）：

```go
// emit 阻塞式投递：消费者总是 range 到 close（runtime.run / RunChild 均如此），
// 故缓冲满时阻塞等待即可、绝不丢事件。ctx.Done 只作为逃生门（消费者停滞时
// 避免永久卡死）。先做一次非阻塞投递：保证“缓冲有空位 + ctx 已取消”场景
// （典型：drain 的 run.cancelled）确定性送达，而非 select 随机二选一丢弃。
func (r *EinoRunner) emit(ctx context.Context, out chan<- RuntimeEvent, runID string, typ domain.EventType, text, tool, payload string) {
	ev := RuntimeEvent{Type: typ, Text: text, ToolName: tool, Payload: payload, RunID: runID}
	select {
	case out <- ev:
	default:
		select {
		case out <- ev:
		case <-ctx.Done():
			return
		}
	}
	if r.onEvent != nil {
		r.onEvent(ev)
	}
}
```

**14 处调用点**机械加 `ctx,` 第一参：
- `runner_child.go:122`（RunChild 的 ctx 在作用域内）
- `runner_eino.go` executeNew goroutine：`:222`
- `runner_eino.go` executeResume goroutine：`:242`、`:245`
- `runner_eino.go` drain：`:260`、`:273`、`:278`、`:280`、`:281`、`:293`（onDelta 闭包捕获 drain 的 ctx）、`:299`、`:303`、`:307`、`:313`

通道容量保持 64 不变。onEvent（DB 落库路径）仅在投递成功后调用 —— 与现状一致。

- [ ] **Step 8.4: 运行确认通过**

Run: `cd backend && go test ./internal/runtime/ -v -timeout 120s && cd backend && go test ./tests/integration/ -timeout 300s`
Expected: 新 2 测试 **PASS**；runtime + 集成全绿（SSE/取消/审批路径回归）。

- [ ] **Step 8.5: Commit**

```bash
git add backend/internal/runtime/runner_eino.go backend/internal/runtime/runner_child.go backend/internal/runtime/runner_eino_test.go
git commit -m "fix(runtime): emit blocks instead of dropping events when buffer full"
```

---

### Task 9: 事件落库错误日志 + Deps.Log（方向 E-3）

**Files:**
- Modify: `backend/internal/runtime/runtime.go`（Deps 加字段、New 兜底、OnEvent/append/appendBudget/appendApproval 错误处理）
- Modify: `backend/internal/app/app.go:80`（传 Log）

- [ ] **Step 9.1: 测试策略说明（无新测试的正当理由）**

`EventRepo` 是具体类型 `*store.EventRepo`，非接口，无法注入失败实现做单元测试；nil-Log 兜底路径由 `tests/testutil/harness.go:259`（Deps 不带 Log）在**全部集成测试**中天然覆盖。本任务为行为增强 + 编译期保障，验证靠 Step 9.4 全量套件。

- [ ] **Step 9.2: 实现**

`runtime.go`：

1. import 加 `"log/slog"`。
2. `Deps` 加字段：

```go
type Deps struct {
	Tasks     *store.TaskRepo
	Events    *store.EventRepo
	Approvals *store.ApprovalRepo
	Runner    *EinoRunner
	Log       *slog.Logger // 可空：缺省 slog.Default()
}
```

3. `New` 开头（OnEvent 注册之前）加兜底：

```go
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
```

4. OnEvent 闭包改为记错误：

```go
	deps.Runner.OnEvent(func(ev RuntimeEvent) {
		payload, _ := json.Marshal(map[string]string{"text": ev.Text, "tool": ev.ToolName})
		if _, err := deps.Events.Append(context.Background(), ev.RunID, ev.Type, string(payload), "normal"); err != nil {
			deps.Log.Warn("event append failed", "run_id", ev.RunID, "type", ev.Type, "err", err)
			observability.IncrEventAppendFailed()
		}
	})
```

5. `append` / `appendBudget` / `appendApproval` 三处 `_, _ =` 替换为同一模式：

```go
	if _, err := r.deps.Events.Append(ctx, runID, typ, string(payload), "normal"); err != nil {
		r.deps.Log.Warn("event append failed", "run_id", runID, "type", typ, "err", err)
		observability.IncrEventAppendFailed()
	}
```

（`appendBudget` 的参数列表已在 Task 3 改为 tracker 版，此处仅改 Append 错误处理那一行。）

`app.go:80` Deps 字面量加一行 `Log: a.log,`。

`harness.go:259` **不改**（走 nil 兜底）。

- [ ] **Step 9.3: 编译检查**

Run: `cd backend && go build ./... && go vet ./internal/runtime/ ./internal/app/`
Expected: 无错误、无告警。

- [ ] **Step 9.4: Commit**

```bash
git add backend/internal/runtime/runtime.go backend/internal/app/app.go
git commit -m "feat(runtime): log and count event append failures via Deps.Log"
```

---

### Task 10: 全量验证

- [ ] **Step 10.1: 后端全量**

Run: `cd backend && go build ./... && go vet ./... && go test ./... -count=1 -timeout 600s`
Expected: build/vet 零输出；全部测试 **PASS**。任何失败先判断是否为本计划引入（对照改动面），**禁止**删测试或放宽断言迁就。

- [ ] **Step 10.2: 前端零改动验证**

Run: `cd frontend && npx tsc --noEmit && npx vitest run`
Expected: tsc 零错误；vitest 全绿（本计划不应触碰 frontend/）。

- [ ] **Step 10.3: 对照 spec 验收清单**

逐条核对 `docs/superpowers/specs/2026-10-26-backend-optimization-design.md` §验收（1-9）：A 终态化+error_code+budget.exceeded、B 幂等返回真实值、C resolveResumeBudget、D 文件库并发/:memory: 单连接、E emit 不丢/append 有日志有计数/延迟桶、公开 API 不变、schema 不变、`git status` 仅含计划内文件。

- [ ] **Step 10.4: 最终确认**

Run: `git log --oneline -8 && git status`
Expected: 8-9 个 commit（Task 1-9 各一），工作区干净。
