# 实时进度精确化 — 设计文档

- **日期**: 2026-10-22
- **范围**: 后端新增 `budget.consumed` 事件(步骤边界发射精确累计) + 前端进度派生**优先**取该事件值(截断免疫),否则回退现有 `.completed` 计数。修掉 `MAX_EVENTS=500` 头部截断导致的 tool/model 计数低估,使运行中进度 gauge 在长 run / 刷新重连后仍精确。
- **状态**: 设计已确认。
- **上级 spec**: 兑现 `2026-10-15-run-progress-design.md` 留的 follow-up("若后续要跨 run/刷新后仍精确,再上后端 `budget.consumed`")。

## 路线取舍(关键)

`2026-10-15-run-progress` 采用纯前端派生:`chat-messages.ts` 数 `eventsByRun[run.id]` 里的 `tool.completed`/`model.completed` 条数,配合 run 时间戳算时长。已知限制:事件流被 `MAX_EVENTS=500` **头部截断**(丢最旧,`run-store.ts:78`),长 run 里 `tool.completed`/`model.completed` 可能部分被丢 → 计数**低估**。

本 spec 补上后端权威计数:

- **后端**:在 `runtime.go` 的 `run()` 事件循环里,已按类型聚合(`toolEvidence++` 数 tool、`finalText` 取模型文本)。新增 `modelCalls`(数 `model.completed`)。每个 tool/model 边界,`append(EvtBudgetConsumed, 累计 snapshot)`,payload `{"model_calls":N,"tool_calls":N,"duration_seconds":N}`。零新管道——复用现成 `r.append`(自动持久化 + 经 `eventshub` 实时推送)。
- **前端**:`chat-messages.ts` 的 `emitRun`,`isActiveRunStatus` 分支取 `eventsByRun[run.id]` 里**最后一条** `budget.consumed`(存在则 `eventPayload` 解析)→ 用其 `model_calls`/`tool_calls`(精确、截断免疫);**无**该事件时回退现有 `.completed` 计数(旧 run / 特征上线前的数据)。
- **截断免疫原理**:`MAX_EVENTS` 丢最旧,`budget.consumed` 每次自增发**最新一条**,累计值单调递增 → 截断后存活的是"最新那条",其 payload 即当前精确累计。
- **语义对齐**:gauge 数 `model.completed`(模型回合完成,不含纯 tool-call 无文本轮次),与现有前端 gauge 口径一致;子运行走独立 `runID`,`eventsByRun` 按 `run_id` 分桶,父 gauge 天然只数顶层,与"子运行保持折叠"策略一致。

## 改动

### 1. `backend/internal/domain/event.go` — 事件词汇

`EventType` 常量组加:

```go
EvtBudgetConsumed    EventType = "budget.consumed"  // cumulative usage at step boundary (exact)
```

放在 `EvtModelToken` 附近(进度/用量语义相邻)。

### 2. `backend/internal/runtime/runtime.go` — 发射点

`run()` 的事件循环(`:124-137`)现状:

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
    ...
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

新增辅助(贴 `append` 旁):

```go
// appendBudget 在步骤边界发 budget.consumed,累计 model_calls/tool_calls/duration。
func (r *Runtime) appendBudget(ctx context.Context, runID string, modelCalls, toolCalls int, started time.Time) {
    payload, _ := json.Marshal(map[string]int{
        "model_calls":      modelCalls,
        "tool_calls":       toolCalls,
        "duration_seconds": int(time.Since(started).Seconds()),
    })
    _, _ = r.deps.Events.Append(ctx, runID, domain.EvtBudgetConsumed, string(payload), "normal")
}
```

`time` 已在 `runtime.go` import;`json` 已在 import。零新 import。

> 说明:`append` 用 `context.Background()` 兜底(见 `runtime.go:144-149` 注释:deadline 死 ctx 时写会静默 no-op)。`appendBudget` 在循环内用 `ctx`(run 正常进行,ctx 活着);循环在 ctx 死亡前已被 `iter.Next()` 的 `<-ctx.Done()` 分支收尾,故边界发射用的是活 ctx,安全。

### 3. `frontend/src/lib/chat-messages.ts` — 派生优先取精确值

`emitRun` 的 `isActiveRunStatus` 分支(`:114-121`),在算完 `.completed` 计数后,若存在 `budget.consumed` 则覆盖为精确值:

```ts
if (isActiveRunStatus(run.status)) {
  const evs = eventsByRun[run.id] ?? []
  let toolCalls = evs.filter((e) => e.event_type === 'tool.completed').length
  let modelCalls = evs.filter((e) => e.event_type === 'model.completed').length
  const startMs = run.started_at ?? run.created_at
  const seconds = startMs > 0 ? Math.max(0, Math.round((now - startMs) / 1000)) : 0
  // 优先:最新的 budget.consumed(后端权威累计,截断免疫)。MAX_EVENTS 丢最旧,
  // 最新一条恒存活,其 payload 即当前精确累计。
  const consumed = evs.filter((e) => e.event_type === 'budget.consumed')
  if (consumed.length > 0) {
    const p = eventPayload(consumed[consumed.length - 1])
    const mc = Number(p.model_calls)
    const tc = Number(p.tool_calls)
    if (Number.isFinite(mc)) modelCalls = mc
    if (Number.isFinite(tc)) toolCalls = tc
  }
  progress = { toolCalls, modelCalls, seconds }
  ...
}
```

`eventPayload`(`status.ts`)已 import(现用于 `model.token` partial 解析)。`SystemStatusBubble` 渲染不变(仍读 `progress`)。

## 数据流

```
runtime.run() 事件循环: 每个 tool/model 边界 → appendBudget → Events.Append(budget.consumed, 累计)
  → (持久化 agent_events) + (eventshub.Publish 实时推送, 上轮刚上线)
→ SSE: handleEvents push case / 1s ticker → client appendEvent (run-store, (seq,type) 去重, MAX_EVENTS 头部截断)
→ ChatMessageList groupEventsByRun → emitRun: 取最后一条 budget.consumed 的 model_calls/tool_calls(精确)
  → progress → SystemStatusBubble 渲染 "工具 x/y · 模型 m/n · Ns"
```

刷新/重连:client 从 `run-store` 重新拉取事件(经 `api.getRun`/SSE replay,≤500 条),最新 `budget.consumed` 存活 → 精确。

## 错误 / 边界

| 场景 | 行为 |
|---|---|
| 无 tool/model 边界(刚 submit / 纯文本 run) | 无 `budget.consumed`;回退 `.completed` 计数(可能 0),`seconds` 正常 |
| 事件 > MAX_EVENTS(500) | `tool.completed`/`model.completed` 被头部丢弃,但**最后一条** `budget.consumed` 存活 → 精确值(截断免疫核心) |
| 旧 run / 特征前数据(无 `budget.consumed`) | `consumed.length === 0` → 回退现有 `.completed` 计数(向后兼容,零破坏) |
| payload 缺字段 / 非数字 | `Number.isFinite` 守卫,缺则保持 `.completed` 回退值 |
| 子运行 | 走独立 `runID`,`eventsByRun[父.run.id]` 不含子事件 → 父 gauge 只数顶层,与折叠策略一致 |
| Resume / Retry | 新 `run()` 调用,`modelCalls`/`toolEvidence`/`started` 从 0 重起 → 计数跨 attempt 不累计(有意,见 §明确不做) |
| duration_seconds | `time.Since(started)` 的 run 内墙钟秒(非跨 attempt),int 截断;gauge 展示用,非权威时长(权威仍 `run.started_at`) |

## 测试

### 后端集成 `backend/tests/integration/budget_consumed_test.go`(新,package `integration`,复用 `testutil` + `sse_follow_test.go` 的 `openStream`/`waitLine`)

```go
func TestBudgetConsumedEmit(t *testing.T) {
    h := testutil.NewHubHarness(t)
    h.Model.EnqueueToolCall("tc1", "publish_draft", `{"title":"hi"}`) // tool+model 边界
    // 或 EnqueueText 走纯 model 边界;按需构造至少 1 model + 1 tool
    ses := h.CreateSession("s")
    id, code, _ := h.CreateRun(ses, map[string]any{"goal": "g"}, "bc-1")
    if code != 201 { t.Fatalf("create run: %d", code) }
    // 等 run 到终态(或 park),再断言 agent_events:
    //   budget.consumed 序列 model_calls/tool_calls 单调不减,
    //   终值 == 该 run 的 model.completed / tool.completed 条数,
    //   payload 三键齐全(duration_seconds >= 0)。
    ...
}
```

断言走 `h.Events`/直接查 `agent_events`(比 SSE 更确定):
- 每条 `budget.consumed` 的 `tool_calls` ≥ 前一条(单调),`model_calls` 同;
- 最后一条的 `model_calls` == `count(event_type='model.completed' AND run_id=id)`;
- 最后一条的 `tool_calls` == `count(event_type='tool.completed' AND run_id=id)`;
- payload 含 `model_calls`/`tool_calls`/`duration_seconds` 三键。

(若需 park 保证事件稳定,复用 `publishToolDef`+`waitApproval`+approve 模式;但断言基于 DB,非 SSE 时序,故不必 `waitLine`。)

### 前端 `frontend/src/lib/chat-messages.test.ts`(扩展现有 `deriveMessages` 用例)

- run 事件流含若干 `tool.completed`/`model.completed` **且** 一条 `budget.consumed`(payload `{"model_calls":3,"tool_calls":2,"duration_seconds":5}`)→ `system-status.progress = {toolCalls:2, modelCalls:3, seconds:...}`(取精确值,即便 `.completed` 条数被造得小于精确值也取精确值——构造 `.completed` 少于 payload 值以证明"优先取 budget.consumed")。
- 同 shape **无** `budget.consumed` 的 run → `progress` 回退 `.completed` 计数(证明向后兼容)。
- `budget.consumed` payload 缺 `tool_calls` 键 → `toolCalls` 保持 `.completed` 回退值(`modelCalls` 仍取精确,若其存在)。

### 验收

- 全量 `cd backend && go build ./... && go vet ./... && go test ./... -count=1` 绿(无 FAIL)。
- 全量 `cd frontend && npx tsc --noEmit && npx vitest run` 绿。
- `git diff master` 含且仅含:`backend/internal/domain/event.go`、`backend/internal/runtime/runtime.go`、`backend/tests/integration/budget_consumed_test.go`、`frontend/src/lib/chat-messages.ts`(+ `chat-messages.test.ts`)。**不含** `SystemStatusBubble`(渲染层不变)、不含其它文件。

## 明确不做(YAGNI)

- **不做跨 attempt / Resume 累计恢复**:Resume/Retry 后计数从 0 重起;跨 attempt 需查库取最后一条 `budget.consumed` 作初值 + 定义跨 attempt 语义,留作 follow-up(文档标注即可)。
- **不做预算强制**:`BudgetTracker`(`runtime/budget.go`,现为死代码 `_ = tracker`)仍不动——"精确计数"与"强制上限"是两个关注点;强制是独立需求(可下轮单独立项)。
- 不做 iteration 计数进 gauge(只 model/tool/duration 三量,与现有渲染一致)。
- 不做前端节流/防抖(每边界发 + 每事件重算,量级小,`system-status` 本就随事件重算)。
- 不改 SSE 协议、不改 `handleEvents`(推/拉两条路径对新事件类型透明,`eventshub` 自动推、`appendEvent` 自动去重)。
