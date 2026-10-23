# budget.consumed 精确进度 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 后端在 tool/model 步骤边界发射 `budget.consumed` 累计事件;前端进度 gauge 优先取该事件的精确值(截断免疫),无则回退现有 `.completed` 计数。

**Architecture:** 后端只改 `runtime.go` 的 `run()` 事件循环(新增 `modelCalls` 计数 + 边界调 `appendBudget` 走 `Events.Append`,自动持久化 + `eventshub` 推送)+ `event.go` 加一个常量。前端改 `types.ts` 联合类型 + `status.ts` `eventSummary` case + `chat-messages.ts` `emitRun` 优先取最后一条 `budget.consumed`。截断免疫原理:`MAX_EVENTS=500` 丢最旧,累计型事件每次自增发最新一条,最新那条恒存活。

**Tech Stack:** Go 1.27(module `rivus-agent-backend`,CGO-free modernc sqlite,`go build/vet/test`);React/Vite/TypeScript + Zustand(frontend/),`npx tsc --noEmit` + `npx vitest run`。

**Spec:** `docs/superpowers/specs/2026-10-22-budget-consumed-design.md`

---

## File Structure

- Modify: `backend/internal/domain/event.go` — 加 `EvtBudgetConsumed` 常量(1 行)
- Modify: `backend/internal/runtime/runtime.go` — `run()` 循环加 `modelCalls` + 两个边界调 `appendBudget`;新增 `appendBudget` 方法(贴 `append` 旁)
- Create: `backend/tests/integration/budget_consumed_test.go` — 单 attempt 夹具(`knowledge_search`,无审批),DB 断言
- Modify: `frontend/src/lib/types.ts` — `EventType` union 加 `'budget.consumed'`
- Modify: `frontend/src/lib/status.ts` — `eventSummary` 加 case
- Modify: `frontend/src/lib/chat-messages.ts` — `emitRun` progress 派生优先取 `budget.consumed`
- Modify: `frontend/src/lib/chat-messages.test.ts` — 3 个新用例(精确优先/回退/缺键)
- Modify: `frontend/src/lib/status.test.ts` — `eventSummary` 2 个新用例

**关键不变量(测试守护):**
- 单 attempt 内:`budget.consumed` 行数 == `model.completed` 数 + `tool.completed` 数;序列内 `model_calls`/`tool_calls` 单调不减;最后一行 == DB 总计数。
- 跨 attempt(Resume/Retry)计数从 0 重起(有意,spec §明确不做)——故后端测试**只用单 attempt 夹具**;不要改用 park/approve 夹具(第二个 `run()` 调用会破坏单调性不变量)。

## Chunk 1: 后端(事件词汇 + 发射点 + 集成测试)

### Task 1: `EvtBudgetConsumed` 事件词汇 + `runtime.go` 发射

**Files:**
- Modify: `backend/internal/domain/event.go:19`(`EvtModelToken` 之后插一行)
- Modify: `backend/internal/runtime/runtime.go:120-137`(事件循环)+ `:184-187`(`append` 旁加 `appendBudget`)

- [ ] **Step 1: 加事件常量**

`backend/internal/domain/event.go`,在 `EvtModelToken` 行(:19)后加:

```go
	EvtBudgetConsumed    EventType = "budget.consumed"  // cumulative usage at step boundary (exact)
```

- [ ] **Step 2: 改 `run()` 事件循环 + 加 `appendBudget`**

`backend/internal/runtime/runtime.go` :120 起,现状:

```go
	toolEvidence := 0
	finalText := ""
	waitApproval := false
	modelFailed := false
	for ev := range ch {
		switch ev.Type {
		case domain.EvtToolCompleted:
			toolEvidence++
		case domain.EvtApprovalRequested:
			waitApproval = true
		case domain.EvtModelCompleted:
			finalText = ev.Text
		case domain.EvtModelFailed:
			modelFailed = true
		case domain.EvtRunFailed:
			finalText = ev.Text
		}
	}
```

改为:

```go
	toolEvidence := 0
	modelCalls := 0
	finalText := ""
	waitApproval := false
	modelFailed := false
	started := time.Now()
	for ev := range ch {
		switch ev.Type {
		case domain.EvtToolCompleted:
			toolEvidence++
			r.appendBudget(ctx, run.ID, modelCalls, toolEvidence, started)
		case domain.EvtApprovalRequested:
			waitApproval = true
		case domain.EvtModelCompleted:
			finalText = ev.Text
			modelCalls++
			r.appendBudget(ctx, run.ID, modelCalls, toolEvidence, started)
		case domain.EvtModelFailed:
			modelFailed = true
		case domain.EvtRunFailed:
			finalText = ev.Text
		}
	}
```

`append` 方法(:184)旁加:

```go
// appendBudget 在 tool/model 步骤边界发 budget.consumed(累计
// model_calls/tool_calls/duration_seconds)。payload 为结构化 JSON,故直接走
// Events.Append(与 append 同一持久化 + eventshub 推送路径)。单 attempt 内
// 计数从 0 单调递增;跨 attempt(Resume/Retry)重起(有意,见 spec)。
func (r *Runtime) appendBudget(ctx context.Context, runID string, modelCalls, toolCalls int, started time.Time) {
	payload, _ := json.Marshal(map[string]int{
		"model_calls":      modelCalls,
		"tool_calls":       toolCalls,
		"duration_seconds": int(time.Since(started).Seconds()),
	})
	_, _ = r.deps.Events.Append(ctx, runID, domain.EvtBudgetConsumed, string(payload), "normal")
}
```

`time`、`json`、`context` 均已在 `runtime.go` import(见 :1-12),零新 import。ctx 安全性:循环内 `appendBudget` 用活 `ctx`(循环只在 `ctx.Done()` 分支或 channel 关闭时结束,见 `drain` `runner_eino.go:253-314`);循环外的 `run.timed_out`/失败分支不经 `appendBudget`,无死 ctx 写问题(那些分支已用 `bgCtx`,见 :143-153)。

- [ ] **Step 3: 编译 + vet + 现有测试不回归**

```bash
cd /data/home/quintin/workspace/source/go/rivus-agent/backend
go build ./... && go vet ./... && go test ./internal/... -count=1 2>&1 | grep -E "FAIL|ok " | tail
```

Expected: 全部 `ok`,无 FAIL。

- [ ] **Step 4: 提交**

```bash
cd /data/home/quintin/workspace/source/go/rivus-agent
git add backend/internal/domain/event.go backend/internal/runtime/runtime.go
git commit -m "feat(runtime): emit budget.consumed at tool/model step boundaries"
```

### Task 2: 集成测试(单 attempt,`knowledge_search`)

**Files:**
- Create: `backend/tests/integration/budget_consumed_test.go`

- [ ] **Step 1: 写测试(完整内容)**

```go
package integration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/tests/testutil"
)

// 单 attempt 夹具:knowledge_search(内置只读 ApprovalNever,无审批,run 在一个
// run() 调用内完成 → 计数单调)。断言基于 DB(非 SSE 时序),故不用 openStream/waitLine。
// 不要改用 park/approve(publishToolDef)夹具:approve 后的 Resume 是第二个 run()
// 调用,计数器从 0 重起,会破坏下面的"单调 + 终值==DB总计数"不变量。
func TestBudgetConsumedEmit(t *testing.T) {
	h := testutil.New(t)
	h.Model.EnqueueToolCall("tc1", "knowledge_search", `{"query":"重构","top_k":3}`)
	h.Model.EnqueueText("输出风险列表:N+1 查询(证据:knowledge_search 结果),每条建议有证据。")
	ses := h.CreateSession("s")
	id, code, _ := h.CreateRun(ses, fixturePayload(t, ses), "bc-1")
	if code != 201 {
		t.Fatalf("create run: %d", code)
	}
	run := h.WaitTerminal(id, 30*time.Second)
	if run.Status != domain.RunSucceeded {
		t.Fatalf("want succeeded, got %s (%s)", run.Status, run.ErrorSummary)
	}

	all, err := h.Events.ListAfter(context.Background(), id, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	var modelCompleted, toolCompleted, consumed []domain.AgentEvent
	for _, ev := range all {
		switch ev.Type {
		case domain.EvtModelCompleted:
			modelCompleted = append(modelCompleted, ev)
		case domain.EvtToolCompleted:
			toolCompleted = append(toolCompleted, ev)
		case domain.EvtBudgetConsumed:
			consumed = append(consumed, ev)
		}
	}

	// 非空真:确有 tool + model 边界
	// (实测:模型每轮各发 1 条 model.completed → 工具轮 + 文本轮共 2 条;
	//  文本轮后循环结束,无兜底 "done" 调用)
	if len(modelCompleted) != 2 {
		t.Fatalf("model.completed count = %d, want 2", len(modelCompleted))
	}
	if len(toolCompleted) != 1 {
		t.Fatalf("tool.completed count = %d, want 1", len(toolCompleted))
	}

	// budget.consumed 行数 == 边界总数(每条 completed 恰发 1 行;本夹具为 3)
	if len(consumed) != len(modelCompleted)+len(toolCompleted) {
		t.Fatalf("budget.consumed count = %d, want %d", len(consumed), len(modelCompleted)+len(toolCompleted))
	}

	// 单调不减 + 终值 == DB 总计数 + payload 三键齐全
	var lastMC, lastTC int
	for i, ev := range consumed {
		var p struct {
			ModelCalls      int `json:"model_calls"`
			ToolCalls       int `json:"tool_calls"`
			DurationSeconds int `json:"duration_seconds"`
		}
		if err := json.Unmarshal([]byte(ev.PayloadJSON), &p); err != nil {
			t.Fatalf("row %d: unmarshal: %v", i, err)
		}
		if p.DurationSeconds < 0 {
			t.Fatalf("row %d: duration_seconds negative: %d", i, p.DurationSeconds)
		}
		if p.ModelCalls < lastMC || p.ToolCalls < lastTC {
			t.Fatalf("row %d: non-monotone (%d,%d) < prev (%d,%d)", i, p.ModelCalls, p.ToolCalls, lastMC, lastTC)
		}
		lastMC, lastTC = p.ModelCalls, p.ToolCalls
	}
	if lastMC != len(modelCompleted) || lastTC != len(toolCompleted) {
		t.Fatalf("final budget.consumed = (%d,%d), want (%d,%d)", lastMC, lastTC, len(modelCompleted), len(toolCompleted))
	}
}

// 纯文本 run:无 tool 边界 → 仅 1 条 budget.consumed (model_calls=1, tool_calls=0)
func TestBudgetConsumedTextOnlyRun(t *testing.T) {
	h := testutil.New(t)
	h.Model.EnqueueText("输出风险列表:N+1 查询(证据:文件),每条建议有文件或代码证据。")
	ses := h.CreateSession("s")
	id, code, _ := h.CreateRun(ses, fixturePayload(t, ses), "bc-text-1")
	if code != 201 {
		t.Fatalf("create run: %d", code)
	}
	run := h.WaitTerminal(id, 30*time.Second)
	if run.Status != domain.RunSucceeded {
		t.Fatalf("want succeeded, got %s (%s)", run.Status, run.ErrorSummary)
	}
	all, err := h.Events.ListAfter(context.Background(), id, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	var consumed []domain.AgentEvent
	for _, ev := range all {
		if ev.Type == domain.EvtBudgetConsumed {
			consumed = append(consumed, ev)
		}
	}
	if len(consumed) != 1 {
		t.Fatalf("budget.consumed count = %d, want 1", len(consumed))
	}
	var p struct {
		ModelCalls int `json:"model_calls"`
		ToolCalls  int `json:"tool_calls"`
	}
	if err := json.Unmarshal([]byte(consumed[0].PayloadJSON), &p); err != nil {
		t.Fatal(err)
	}
	if p.ModelCalls != 1 || p.ToolCalls != 0 {
		t.Fatalf("payload = (%d,%d), want (1,0)", p.ModelCalls, p.ToolCalls)
	}
}
```

注:`fixturePayload`、`knowledge_search`、`WaitTerminal`、`ListAfter` 均已在包内/`testutil` 存在(`api_test.go:53,65`、`harness.go:349`、`event_repo.go:49`);`testutil.New`(无 hub)足够——断言走 DB,不经 SSE,`budget.consumed` 持久化与 hub 无关。

- [ ] **Step 2: 跑新测试(此时应已 PASS——Task 1 已实现;这是对实现的守护)**

```bash
cd /data/home/quintin/workspace/source/go/rivus-agent/backend
go test ./tests/integration/ -run 'TestBudgetConsumed' -v -count=1 2>&1 | tail -15
```

Expected: `TestBudgetConsumedEmit` PASS、`TestBudgetConsumedTextOnlyRun` PASS。

- [ ] **Step 3: 反向验证(确认测试真守护行为)**

临时注释掉 `runtime.go` 循环里两处 `r.appendBudget(...)` 调用,重跑:

```bash
cd /data/home/quintin/workspace/source/go/rivus-agent/backend
go test ./tests/integration/ -run 'TestBudgetConsumedEmit' -count=1 2>&1 | tail -3
```

Expected: FAIL(`budget.consumed count = 0, want 3`)。验证完**还原**那两行(勿提交改动)。

- [ ] **Step 4: 提交**

```bash
cd /data/home/quintin/workspace/source/go/rivus-agent
git add backend/tests/integration/budget_consumed_test.go
git commit -m "test(integration): budget.consumed emit at step boundaries (single-attempt, DB assertions)"
```

## Chunk 2: 前端(类型 + 摘要 + 派生优先精确值)

### Task 3: `types.ts` + `status.ts`

**Files:**
- Modify: `frontend/src/lib/types.ts:19-45`(`EventType` union)
- Modify: `frontend/src/lib/status.ts:132`(`eventSummary` switch)
- Test: `frontend/src/lib/status.test.ts`

- [ ] **Step 1: union + eventSummary case**

`types.ts` `EventType` union(在 `'model.token'` 行 :33 旁,进度/用量语义相邻):

```ts
  | 'budget.consumed'  // cumulative usage at step boundary (backend authoritative)
```

`status.ts` `eventSummary` switch(贴 `'model.token'` case 之后;`p` 即 `eventPayload(e)`,`Record<string, unknown>`):

```ts
    case 'budget.consumed':
      return p.tool_calls != null
        ? `用量 工具 ${String(p.tool_calls)}/模型 ${String(p.model_calls ?? 0)}`
        : '用量更新'
```

(不加 union 成员则 §Task 4 的 `e.event_type === 'budget.consumed'` 过滤在 `tsc --noEmit` 下 TS2367;不加 `eventSummary` case 则折叠事件列表渲染 "Unknown event budget.consumed"。)

- [ ] **Step 2: `status.test.ts` 加 2 个用例**

在 `describe('eventSummary', ...)` 内(`status.test.ts:38` 的 unknown-event 测试旁):

```ts
  it('summarizes budget.consumed with usage counts', () => {
    expect(eventSummary(ev('budget.consumed', { model_calls: 3, tool_calls: 2 }))).toBe('用量 工具 2/模型 3')
  })

  it('falls back to 用量更新 when tool_calls absent on budget.consumed', () => {
    expect(eventSummary(ev('budget.consumed', { model_calls: 3 }))).toBe('用量更新')
  })
```

(`ev` helper 在 `status.test.ts:5-7`,已存在;`tool_calls` 缺省 → `p.tool_calls == null` → 走 `'用量更新'`。)

- [ ] **Step 3: 编译 + 该测试文件绿**

```bash
cd /data/home/quintin/workspace/source/go/rivus-agent/frontend
npx tsc --noEmit && npx vitest run src/lib/status.test.ts 2>&1 | tail -8
```

Expected: tsc 无错;status.test 全 PASS(含新增 2 条)。

- [ ] **Step 4: 提交**

```bash
cd /data/home/quintin/workspace/source/go/rivus-agent
git add frontend/src/lib/types.ts frontend/src/lib/status.ts frontend/src/lib/status.test.ts
git commit -m "feat(frontend): budget.consumed event type + eventSummary case"
```

### Task 4: `chat-messages.ts` 派生优先取精确值

**Files:**
- Modify: `frontend/src/lib/chat-messages.ts:113-121`(`emitRun` `isActiveRunStatus` 分支)
- Test: `frontend/src/lib/chat-messages.test.ts`(追加在 :293 现有 progress 测试之后)

- [ ] **Step 1: 写 3 个新测试(先 RED)**

`chat-messages.test.ts` 的 `describe('deriveMessages')` 内,:293(`falls back to created_at...` 用例)之后加:

```ts
  it('prefers the last budget.consumed for progress over .completed counts', () => {
    const startMs = 1_000_000
    const now = startMs + 2000
    const run = mkRun('r1', 'running', { started_at: startMs })
    // .completed 计数被造得少于 budget.consumed 的精确值(模拟 MAX_EVENTS 截断低估)
    const evs = [
      mkEv(1, 'tool.completed'),
      mkEv(2, 'model.completed'),
      mkEv(3, 'budget.consumed', '{"model_calls":5,"tool_calls":4,"duration_seconds":19}'),
    ]
    const out = deriveMessages({ runHistory: [], activeRun: run, activeSteps: [], eventsByRun: { r1: evs }, pendingGoals: [], now })
    const status = out.find((m) => m.kind === 'system-status')
    expect(status).toMatchObject({ progress: { toolCalls: 4, modelCalls: 5, seconds: 2 } })
  })

  it('falls back to .completed counts when no budget.consumed (legacy data)', () => {
    const startMs = 1_000_000
    const now = startMs + 2000
    const run = mkRun('r1', 'running', { started_at: startMs })
    const evs = [
      mkEv(1, 'tool.completed'),
      mkEv(2, 'tool.completed'),
      mkEv(3, 'model.completed'),
    ]
    const out = deriveMessages({ runHistory: [], activeRun: run, activeSteps: [], eventsByRun: { r1: evs }, pendingGoals: [], now })
    const status = out.find((m) => m.kind === 'system-status')
    expect(status).toMatchObject({ progress: { toolCalls: 2, modelCalls: 1, seconds: 2 } })
  })

  it('applies budget.consumed partial payload (missing tool_calls keeps fallback)', () => {
    const startMs = 1_000_000
    const now = startMs + 2000
    const run = mkRun('r1', 'running', { started_at: startMs })
    const evs = [
      mkEv(1, 'tool.completed'),
      mkEv(2, 'tool.completed'),
      mkEv(3, 'budget.consumed', '{"model_calls":7,"duration_seconds":19}'), // 缺 tool_calls
    ]
    const out = deriveMessages({ runHistory: [], activeRun: run, activeSteps: [], eventsByRun: { r1: evs }, pendingGoals: [], now })
    const status = out.find((m) => m.kind === 'system-status')
    expect(status).toMatchObject({ progress: { toolCalls: 2, modelCalls: 7, seconds: 2 } })
  })
```

- [ ] **Step 2: 跑测试确认 RED**

```bash
cd /data/home/quintin/workspace/source/go/rivus-agent/frontend
npx vitest run src/lib/chat-messages.test.ts 2>&1 | tail -15
```

Expected: 新 3 条中第 1、3 条 FAIL(实现还没做 → `toolCalls`/`modelCalls` 走 `.completed` 计数,取不到精确值);第 2 条 PASS(回退路径本就是现有行为)。

- [ ] **Step 3: 实现 `emitRun` 优先精确值**

`chat-messages.ts:113-121`,现状:

```ts
  if (isActiveRunStatus(run.status)) {
    const evs = eventsByRun[run.id] ?? []
    const toolCalls = evs.filter((e) => e.event_type === 'tool.completed').length
    const modelCalls = evs.filter((e) => e.event_type === 'model.completed').length
    const startMs = run.started_at ?? run.created_at
    const seconds = startMs > 0 ? Math.max(0, Math.round((now - startMs) / 1000)) : 0
    progress = { toolCalls, modelCalls, seconds }
```

改为:

```ts
  if (isActiveRunStatus(run.status)) {
    const evs = eventsByRun[run.id] ?? []
    let toolCalls = evs.filter((e) => e.event_type === 'tool.completed').length
    let modelCalls = evs.filter((e) => e.event_type === 'model.completed').length
    const startMs = run.started_at ?? run.created_at
    const seconds = startMs > 0 ? Math.max(0, Math.round((now - startMs) / 1000)) : 0
    // 优先:最新一条 budget.consumed(后端单-attempt 权威累计)。MAX_EVENTS 头部
    // 截断丢最旧,最新那条恒存活 → 截断免疫;缺失(旧数据)则保持 .completed 回退。
    const consumed = evs.filter((e) => e.event_type === 'budget.consumed')
    if (consumed.length > 0) {
      const p = eventPayload(consumed[consumed.length - 1])
      const mc = Number(p.model_calls)
      const tc = Number(p.tool_calls)
      if (Number.isFinite(mc)) modelCalls = mc
      if (Number.isFinite(tc)) toolCalls = tc
    }
    progress = { toolCalls, modelCalls, seconds }
```

(`eventPayload` 已在 :2 import;`Number(undefined) === NaN` 非有限 → 缺键自然保持回退值,无需特判;`Number(null) === 0` 有限 → 显式 0 也采信。)

- [ ] **Step 4: 跑测试确认 GREEN + 全量前端**

```bash
cd /data/home/quintin/workspace/source/go/rivus-agent/frontend
npx vitest run src/lib/chat-messages.test.ts 2>&1 | tail -10
npx tsc --noEmit && npx vitest run 2>&1 | tail -8
```

Expected: 新 3 条 PASS;全量 vitest 绿(含 10-15 轮已有 progress 测试——它们无 `budget.consumed`,走回退路径,断言不变,应仍 PASS)。

- [ ] **Step 5: 提交**

```bash
cd /data/home/quintin/workspace/source/go/rivus-agent
git add frontend/src/lib/chat-messages.ts frontend/src/lib/chat-messages.test.ts
git commit -m "feat(frontend): progress gauge prefers budget.consumed exact counts (truncation-immune)"
```

## Chunk 3: 全量验证

### Task 5: 双栈全量 + diff 面核对

**Files:** 无(纯验证)。

- [ ] **Step 1: 后端全量**

```bash
cd /data/home/quintin/workspace/source/go/rivus-agent/backend
go build ./... && go vet ./... && go test ./... -count=1 2>&1 | grep -E "FAIL|ok " | tail -15
```

Expected: 全部 `ok`(integration 含 `TestBudgetConsumed*`),无 FAIL。

- [ ] **Step 2: 前端全量**

```bash
cd /data/home/quintin/workspace/source/go/rivus-agent/frontend
npx tsc --noEmit && npx vitest run 2>&1 | tail -10
```

Expected: tsc 无错;vitest 全绿。

- [ ] **Step 3: diff 面核对(与 spec §验收一致)**

```bash
cd /data/home/quintin/workspace/source/go/rivus-agent
git diff --name-only master..HEAD 2>/dev/null || git show --name-status --format= HEAD
```

Expected: 恰为 7 个文件——`backend/internal/domain/event.go`、`backend/internal/runtime/runtime.go`、`backend/tests/integration/budget_consumed_test.go`、`frontend/src/lib/types.ts`、`frontend/src/lib/status.ts`(+ test)、`frontend/src/lib/chat-messages.ts`(+ test)。**不含** `SystemStatusBubble`、`run-store`、SSE 相关文件。

---

## Rollback

3 个 commit 各自独立可回退(前端 2 个 commit 之间无编译依赖:`types.ts`/`status.ts` 先合,`chat-messages.ts` 依赖 union 成员——故回退顺序为反序)。回退全部 3 个 commit 后,行为回到"事件计数 + 时间戳时长"的 10-15 进度,零残留。
