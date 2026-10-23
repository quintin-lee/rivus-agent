# 预算强制(让 BudgetTracker 真正生效)Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 `max_tool_calls` / `max_model_calls` 真正被强制:运行中用满即取消 runner,run 落终态 `RunFailed` + `error_code`(tool_calls_budget_exceeded / model_calls_budget_exceeded),前端零改动。

**Architecture:** `BudgetTracker` 从死代码变活为 `run()` 事件循环的唯一计数事实源:本地计数器 `toolEvidence`/`modelCalls` 删除,改 `tracker.AddToolCall()`/`AddModelCall()`;每轮事件后问一次新 `tracker.OverBudget()`,首次命中调 `r.deps.Runner.Cancel(run.ID)`(Stop 按钮同源路径);循环后预算分支用 `context.Background()` 写终态(防死 ctx 静默 no-op)。`appendBudget` 改读 `tracker.Snapshot()`,发出的 `budget.consumed` 值/行序/三键与已合版本逐字节一致(既有集成测试保持绿)。

**Tech Stack:** Go(module `rivus-agent-backend`,modernc SQLite);测试 `go test`。TDD:单测先行,集成测试探针钉数。

**Spec:** `docs/superpowers/specs/2026-10-23-budget-enforcement-design.md`(commit `753095d`)

**探针实测基线(既有 `TestBudgetConsumed*` 已验证):** 单 attempt 工具轮+文本轮 run 产生 `model.completed`=2(每模型轮各 1)、`tool.completed`=1、`budget.consumed` 恰 3 行 `(1,0)→(1,1)→(2,1)`;纯文本 run 恰 1 行 `(1,0)`。

---

## 前置事实(实现前已核实)

- `budget.go:18-59`:`BudgetTracker` 已有 `mu/limit/usage` + `Check*`/`Add*`/`Snapshot`;`OverBudget` **不存在**,需新增。
- `runtime.go:64-65`:`tracker := NewBudgetTracker(budget)` + `_ = tracker`(死代码,删后者)。
- `runtime.go:120-145`:循环本地计数 `toolEvidence := 0`/`modelCalls := 0`/`started := time.Now()`;case `EvtToolCompleted`→`toolEvidence++` + `appendBudget(ctx, run.ID, modelCalls, toolEvidence, started)`;case `EvtModelCompleted`→`finalText = ev.Text; modelCalls++` + 同式 `appendBudget`。
- `runtime.go:160/167/168`:三处 `toolEvidence` 下游读值点(`modelFailed && toolEvidence == 0`、`Verify(..., toolEvidence)`、空结果检查 `... && toolEvidence == 0 && ...`)。
- `runtime.go:143-153`:`timed_out` 分支已确立"死 ctx 必须 `bgCtx` 写 DB"模式,本 plan 预算分支同构。
- `runner_eino.go:128`:`func (r *EinoRunner) Cancel(runID string) error`(`Deps.Runner` 是具体类型 `*EinoRunner`,run() 内可直接调);cancel 后 drain `:259-261` 发一条 `EvtRunCancelled`(Stop byproduct,保留,spec §3=a)。
- `harness.go:323` `CreateRun(sessionID, payload, idemKey)`:payload 即 `fixturePayload(t, ses)`,可覆写 `budget` 字段;`h.GetRun` 返回 `domain.Run`(含 `ErrorCode`);`h.Events.ListAfter` 读 `agent_events`。

## File Structure

- Modify: `backend/internal/runtime/budget.go` — 新增 `OverBudget()`(:50 `CheckTool` 后)
- Create: `backend/internal/runtime/budget_test.go` — `OverBudget`/`Add*`/`Snapshot` 单测
- Modify: `backend/internal/runtime/runtime.go` — 删死代码、循环改 tracker + 闸门、`appendBudget` 读 snapshot、循环后预算分支、三处下游读值
- Create: `backend/tests/integration/budget_cap_test.go` — tool 上限 / model 上限 / 对照组(DB 断言,非 SSE 时序)

---

## Chunk 1: `OverBudget` + 单测

### Task 1: `BudgetTracker.OverBudget` + `budget_test.go`

**Files:**
- Modify: `backend/internal/runtime/budget.go`(`CheckTool` `:43-50` 之后插入)
- Create: `backend/internal/runtime/budget_test.go`

- [ ] **Step 1: 写单测(RED)**

Create `backend/internal/runtime/budget_test.go`:

```go
package runtime

import (
	"testing"

	"rivus-agent-backend/internal/domain"
)

func TestOverBudgetToolCap(t *testing.T) {
	tr := NewBudgetTracker(domain.Budget{MaxToolCalls: 1})
	if code, hit := tr.OverBudget(); hit || code != "" {
		t.Fatalf("before: got (%q,%v), want empty", code, hit)
	}
	tr.AddToolCall()
	code, hit := tr.OverBudget()
	if !hit || code != "tool_calls_budget_exceeded" {
		t.Fatalf("after 1 tool: got (%q,%v), want (tool_calls_budget_exceeded,true)", code, hit)
	}
}

func TestOverBudgetModelCap(t *testing.T) {
	tr := NewBudgetTracker(domain.Budget{MaxModelCalls: 1})
	tr.AddModelCall()
	code, hit := tr.OverBudget()
	if !hit || code != "model_calls_budget_exceeded" {
		t.Fatalf("got (%q,%v), want (model_calls_budget_exceeded,true)", code, hit)
	}
}

func TestOverBudgetToolCheckedFirst(t *testing.T) {
	tr := NewBudgetTracker(domain.Budget{MaxToolCalls: 1, MaxModelCalls: 1})
	tr.AddToolCall()
	tr.AddModelCall()
	code, hit := tr.OverBudget()
	if !hit || code != "tool_calls_budget_exceeded" {
		t.Fatalf("got (%q,%v), want tool_calls_budget_exceeded(true) (tool checked first)", code, hit)
	}
}

func TestOverBudgetUnsetLimitsNotEnforced(t *testing.T) {
	tr := NewBudgetTracker(domain.Budget{}) // 未设上限
	for i := 0; i < 100; i++ {
		tr.AddToolCall()
		tr.AddModelCall()
	}
	if code, hit := tr.OverBudget(); hit || code != "" {
		t.Fatalf("unset limits must not enforce; got (%q,%v)", code, hit)
	}
}

func TestSnapshotReflectsAdds(t *testing.T) {
	tr := NewBudgetTracker(domain.Budget{MaxModelCalls: 10, MaxToolCalls: 10})
	tr.AddModelCall()
	tr.AddModelCall()
	tr.AddToolCall()
	s := tr.Snapshot()
	if s.ModelCalls != 2 || s.ToolCalls != 1 {
		t.Fatalf("snapshot = (%d,%d), want (2,1)", s.ModelCalls, s.ToolCalls)
	}
}
```

- [ ] **Step 2: 跑测试确认 RED**

Run: `cd backend && go test ./internal/runtime/ -run 'TestOverBudget|TestSnapshotReflectsAdds' -count=1`
Expected: FAIL(编译错 `tr.OverBudget undefined`)。

- [ ] **Step 3: 实现 `OverBudget`(GREEN)**

In `budget.go`, after `CheckTool`(:50), add:

```go
// OverBudget 事后判断是否已达任一已设上限(>=,放行 N 个即停)。
// 命中返回 (code, true),code 为要写入 run.error_code 的稳定值;未设上限
// (limit<=0)的维度不强制,返回 ("", false)。tool 先查(两维度同时满只在
// 已取消之后发生,不会同时被检查到)。
func (b *BudgetTracker) OverBudget() (code string, hit bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit.MaxToolCalls > 0 && b.usage.ToolCalls >= b.limit.MaxToolCalls {
		return "tool_calls_budget_exceeded", true
	}
	if b.limit.MaxModelCalls > 0 && b.usage.ModelCalls >= b.limit.MaxModelCalls {
		return "model_calls_budget_exceeded", true
	}
	return "", false
}
```

- [ ] **Step 4: 跑测试确认 GREEN + 编译**

Run: `cd backend && go test ./internal/runtime/ -run 'TestOverBudget|TestSnapshotReflectsAdds' -count=1 && go build ./... && gofmt -l internal/runtime/`
Expected: 5 tests PASS;build 绿;gofmt 无输出。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/runtime/budget.go backend/internal/runtime/budget_test.go
git commit -m "feat(runtime): BudgetTracker.OverBudget — post-hoc tool/model cap check"
```

## Chunk 2: `runtime.go` 接线

### Task 2: `run()` 循环改 tracker + 闸门 + 终态分支

**Files:**
- Modify: `backend/internal/runtime/runtime.go`(`:64-65` 死代码、`:120-145` 循环、`:142` 后插预算分支、`:160/167/168` 读值点、`:198-205` `appendBudget`)

READ `runtime.go` 全文确认锚点(行号可能 ±,以实际代码为准)。

- [ ] **Step 1: 删死代码 + 循环改 tracker + 闸门**

1. `:65` 的 `_ = tracker` 删除。
2. 循环声明区:删 `toolEvidence := 0` 与 `modelCalls := 0`,保留 `started := time.Now()`;新增:
```go
	var budgetCode, budgetMsg string
	var budgetFired bool
```
3. case `EvtToolCompleted`:
```go
		case domain.EvtToolCompleted:
			tracker.AddToolCall()
			r.appendBudget(ctx, run.ID, tracker, started)
```
4. case `EvtModelCompleted`:
```go
		case domain.EvtModelCompleted:
			finalText = ev.Text
			tracker.AddModelCall()
			r.appendBudget(ctx, run.ID, tracker, started)
```
5. switch 块收尾(所有 case 之后、`}` 之前)统一加闸门:
```go
		// 事后强制:达任一已设上限即取消 runner(首次命中一次),落到下方 RunFailed。
		if code, hit := tracker.OverBudget(); hit && !budgetFired {
			budgetFired = true
			budgetCode = code
			budgetMsg = "run stopped: " + code
			_ = r.deps.Runner.Cancel(run.ID)
		}
```

- [ ] **Step 2: `appendBudget` 改读 snapshot**

`:198-205` 改为:

```go
func (r *Runtime) appendBudget(ctx context.Context, runID string, tracker *BudgetTracker, started time.Time) {
	sn := tracker.Snapshot()
	payload, _ := json.Marshal(map[string]int{
		"model_calls":      sn.ModelCalls,
		"tool_calls":       sn.ToolCalls,
		"duration_seconds": int(time.Since(started).Seconds()),
	})
	_, _ = r.deps.Events.Append(ctx, runID, domain.EvtBudgetConsumed, string(payload), "normal")
}
```

(doc comment 保持原意:单 attempt 内从 0 单调;跨 attempt 重起。)

- [ ] **Step 3: 循环后预算终态分支**

在 `if waitApproval || needApproval.Load() {...}` 块**之后**、`if errors.Is(ctx.Err(), context.DeadlineExceeded)` **之前**插入:

```go
	if budgetCode != "" {
		// 与 timed_out 分支同理:外层 deadline 可能恰在同一窗口到期,ctx 或已死,
		// database/sql 对死 ctx 静默 no-op、run 永远停在 running —— DB 写全用 Background()。
		bgCtx := context.Background()
		_ = r.deps.Tasks.UpdateStatus(bgCtx, ownerID, run.ID, domain.RunFailed, budgetCode, budgetMsg)
		r.append(bgCtx, run.ID, domain.EvtRunFailed, budgetMsg)
		observability.IncrRunFinished(false)
		r.deps.Runner.DeleteCheckpointsByRun(bgCtx, run.ID)
		return
	}
```

- [ ] **Step 4: 三处下游 `toolEvidence` 读值改 snapshot**

- `:160` `if modelFailed && toolEvidence == 0` → `if modelFailed && tracker.Snapshot().ToolCalls == 0`
- `:167` `report := Verify(run.SuccessCriteria, finalText, toolEvidence)` → `report := Verify(run.SuccessCriteria, finalText, tracker.Snapshot().ToolCalls)`
- `:168` 空结果检查 `&& toolEvidence == 0 &&` → `&& tracker.Snapshot().ToolCalls == 0 &&`

(删局部变量后这三处不编译,编译器强制全改到;若有遗漏,`go build` 会报 undefined。)

- [ ] **Step 5: 编译 + vet + 回归(既有 budget.consumed 测试必须绿)**

Run:
```bash
cd backend
go build ./... && go vet ./...
go test ./internal/... -count=1 2>&1 | grep -E "FAIL|ok " | tail
go test ./tests/integration/ -run 'TestBudgetConsumed' -count=1 2>&1 | tail -3
```
Expected: build/vet 绿;internal 全 ok;`TestBudgetConsumedEmit`/`TestBudgetConsumedTextOnlyRun` 仍 PASS(snapshot 值与本地计数器逐字节一致)。

- [ ] **Step 6: 提交**

```bash
git add backend/internal/runtime/runtime.go
git commit -m "feat(runtime): enforce tool/model budgets — post-hoc gate cancels runner, maps RunFailed + error_code"
```

## Chunk 3: 集成测试(探针钉数)

### Task 3: `budget_cap_test.go`(tool 上限 / model 上限 / 对照组)

**File:** Create `backend/tests/integration/budget_cap_test.go`

- [ ] **Step 1: 探针实测精确交错数(写死断言前必须跑)**

Create 临时文件 `backend/tests/integration/budget_cap_probe_test.go`:

```go
package integration

import (
	"context"
	"testing"
	"time"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/tests/testutil"
)

// 探针:超限时 Eino cancel 与 tool 执行的交错数(tool.completed 到底 0 还是 1)。
func TestProbeModelCapCounts(t *testing.T) {
	h := testutil.New(t)
	h.Model.EnqueueToolCall("tc1", "knowledge_search", `{"query":"重构","top_k":3}`)
	h.Model.EnqueueText("输出风险列表:N+1 查询(证据:knowledge_search 结果),每条建议有证据。")
	ses := h.CreateSession("s")
	payload := fixturePayload(t, ses)
	payload["budget"] = map[string]any{"max_duration_seconds": 120, "max_model_calls": 1,
		"max_tool_calls": 10, "max_iterations": 6, "max_output_bytes": 50000}
	id, code, _ := h.CreateRun(ses, payload, "probe-modelcap")
	if code != 201 {
		t.Fatalf("create run: %d", code)
	}
	run := h.WaitTerminal(id, 30*time.Second)
	t.Logf("terminal=%s error_code=%s", run.Status, run.ErrorCode)
	all, err := h.Events.ListAfter(context.Background(), id, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, ev := range all {
		counts[string(ev.Type)]++
	}
	t.Logf("counts: model.completed=%d tool.completed=%d run.failed=%d run.cancelled=%d",
		counts["model.completed"], counts["tool.completed"], counts["run.failed"], counts["run.cancelled"])
	_ = domain.RunSucceeded
}
```

Run: `cd backend && go test ./tests/integration/ -run 'TestProbeModelCapCounts' -v -count=1 2>&1 | grep -E "terminal=|counts:"`
记录输出。**据此决定下面 Task 3 各断言的精确数字**(预期:终态 `RunFailed` + `error_code=model_calls_budget_exceeded`;`model.completed` 精确值以探针为准;`tool.completed` 交错数(0 或 1)以探针为准)。

- [ ] **Step 2: 写正式测试(用探针钉数)**

Delete 探针文件,create `backend/tests/integration/budget_cap_test.go`。结构(数字以探针结果填实):

```go
package integration

import (
	"context"
	"testing"
	"time"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/tests/testutil"
)

// capPayload:fixturePayload 覆写 budget(其余字段齐全,防 ClampByServer 归零语义干扰)。
func capPayload(t *testing.T, ses string, maxTool, maxModel int) map[string]any {
	t.Helper()
	p := fixturePayload(t, ses)
	p["budget"] = map[string]any{"max_duration_seconds": 120, "max_model_calls": maxModel,
		"max_tool_calls": maxTool, "max_iterations": 6, "max_output_bytes": 50000}
	return p
}

// tool 上限:2 个 knowledge_search 工具调用,max_tool_calls=1 → 第 1 个完成即取消。
func TestBudgetCapToolCalls(t *testing.T) {
	h := testutil.New(t)
	h.Model.EnqueueToolCall("tc1", "knowledge_search", `{"query":"重构","top_k":3}`)
	h.Model.EnqueueToolCall("tc2", "knowledge_search", `{"query":"索引","top_k":3}`)
	ses := h.CreateSession("s")
	id, code, _ := h.CreateRun(ses, capPayload(t, ses, 1, 10), "cap-tool-1")
	if code != 201 {
		t.Fatalf("create run: %d", code)
	}
	run := h.WaitTerminal(id, 30*time.Second)
	if run.Status != domain.RunFailed || run.ErrorCode != "tool_calls_budget_exceeded" {
		t.Fatalf("want failed/tool_calls_budget_exceeded, got %s/%s", run.Status, run.ErrorCode)
	}
	all, err := h.Events.ListAfter(context.Background(), id, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	toolCompleted, runFailed := 0, 0
	for _, ev := range all {
		if ev.Type == domain.EvtToolCompleted {
			toolCompleted++
		}
		if ev.Type == domain.EvtRunFailed {
			runFailed++
		}
	}
	// 第 1 个工具完成即取消:tool.completed 恰 1,run.failed 恰 1(探针钉数后若为 0 则改)
	if toolCompleted != 1 {
		t.Fatalf("tool.completed = %d, want 1", toolCompleted)
	}
	if runFailed != 1 {
		t.Fatalf("run.failed = %d, want 1", runFailed)
	}
}

// model 上限:工具轮 + 文本轮 = 2 次模型调用,max_model_calls=1 → 第 1 次完成即取消。
func TestBudgetCapModelCalls(t *testing.T) {
	h := testutil.New(t)
	h.Model.EnqueueToolCall("tc1", "knowledge_search", `{"query":"重构","top_k":3}`)
	h.Model.EnqueueText("输出风险列表:N+1 查询(证据:knowledge_search 结果),每条建议有证据。")
	ses := h.CreateSession("s")
	id, code, _ := h.CreateRun(ses, capPayload(t, ses, 10, 1), "cap-model-1")
	if code != 201 {
		t.Fatalf("create run: %d", code)
	}
	run := h.WaitTerminal(id, 30*time.Second)
	if run.Status != domain.RunFailed || run.ErrorCode != "model_calls_budget_exceeded" {
		t.Fatalf("want failed/model_calls_budget_exceeded, got %s/%s", run.Status, run.ErrorCode)
	}
	// (探针钉数:model.completed / tool.completed 精确值填入此处断言;
	//  若探针显示 tool.completed 交错数不稳定,只断言下限 >= 0 并在注释里说明原因)
}

// 对照组:不超限(1 个工具,max_tool_calls=10)→ 正常 succeeded、无 error_code。
func TestBudgetCapWithinLimits(t *testing.T) {
	h := testutil.New(t)
	h.Model.EnqueueToolCall("tc1", "knowledge_search", `{"query":"重构","top_k":3}`)
	h.Model.EnqueueText("输出风险列表:N+1 查询(证据:knowledge_search 结果),每条建议有证据。")
	ses := h.CreateSession("s")
	id, code, _ := h.CreateRun(ses, capPayload(t, ses, 10, 10), "cap-ok-1")
	if code != 201 {
		t.Fatalf("create run: %d", code)
	}
	run := h.WaitTerminal(id, 30*time.Second)
	if run.Status != domain.RunSucceeded {
		t.Fatalf("want succeeded, got %s (%s)", run.Status, run.ErrorSummary)
	}
	if run.ErrorCode != "" {
		t.Fatalf("error_code should be empty on success, got %q", run.ErrorCode)
	}
}
```

**注意:**
- 纯文本 `EnqueueText`×2 只会发生 1 次模型调用(文本响应终止 agent 循环),model 上限必须用"工具轮 + 文本轮"(= 自然 2 次模型调用)。
- 数字(尤其 `tool.completed` 交错数)一律以 Step 1 探针输出为准填入;填不了的写 `t.Logf` 观察值 + 注释说明,不留猜测数字。

- [ ] **Step 3: 跑新测试**

Run: `cd backend && go test ./tests/integration/ -run 'TestBudgetCap' -v -count=1 2>&1 | grep -E "^(--- |ok|FAIL)"`
Expected: 3 tests PASS。跑 3 遍确认稳定(交错数不 flake):
Run: `cd backend && for i in 1 2 3; do go test ./tests/integration/ -run 'TestBudgetCap' -count=1 2>&1 | tail -1; done`

- [ ] **Step 4: 反向验证(测试真守护行为)**

临时把 `runtime.go` 闸门 `if code, hit := tracker.OverBudget(); ...` 整块注释,重跑 `TestBudgetCapToolCalls` 应 FAIL(run 会跑到 `RunSucceeded` 或超时,而非 `tool_calls_budget_exceeded`)。**还原**后确认 `git diff backend/internal/runtime/runtime.go` 为空。

- [ ] **Step 5: 提交**

```bash
git add backend/tests/integration/budget_cap_test.go
git commit -m "test(integration): budget caps enforce tool_calls/model_calls → RunFailed + error_code"
```

## Chunk 4: 全量验证

### Task 4: 双栈全量 + diff 面核对

**Files:** 无(纯验证)。

- [ ] **Step 1: 后端全量**

Run: `cd backend && go build ./... && go vet ./... && go test ./... -count=1 2>&1 | grep -E "FAIL|ok " | tail -15`
Expected: 12 包全 ok(含新增 budget_test + budget_cap_test;既有 TestBudgetConsumed* 保持绿)。

- [ ] **Step 2: 前端(应零改动、全绿)**

Run: `cd frontend && npx tsc --noEmit && npx vitest run 2>&1 | grep -E "Test Files|Tests"`
Expected: tsc 无错;127/127 vitest 通过(本 feature 不动前端)。

- [ ] **Step 3: diff 面核对(与 spec 一致)**

Run: `git diff --name-only master..HEAD`(worktree 分支上)
Expected: 恰 4 文件——`backend/internal/runtime/budget.go`、`backend/internal/runtime/budget_test.go`、`backend/internal/runtime/runtime.go`、`backend/tests/integration/budget_cap_test.go`。**不含** 前端、domain、api/http、SSE 相关。

---

## Rollback

3 个 commit 独立可回退:回退 Task 2(runtime 接线)后 `OverBudget` 变回无调用方(死代码,编译不报错);回退 Task 3(集成测试)无副作用;回退 Task 1(budget.go+单测)删除 `OverBudget`(此时 Task 2 必须已回退)。全部回退 = 回到"仅 duration 强制 + BudgetTracker 死代码"的现状。
