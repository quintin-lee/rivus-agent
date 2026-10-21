# 流式 Token Deltas（§4 follow-up）— 设计文档

- **日期**: 2026-10-21
- **范围**: 让模型结果**实时流式**显示，而非等 run 完成才一次性弹出。动 `backend/internal/domain`（新事件类型）、`backend/internal/runtime/runner_eino.go`（`drain`/`drainStream` 发 delta）、`frontend/src`（累积 + 渲染 live partial）。兑现 `2026-10-14-follow-ups.md` §4。
- **状态**: 设计已确认。

## 背景 / 现状

今天模型结果**只在 run 完成时一次性出现**，中间过程用户看不到。根因在 `runner_eino.go`：`drain` 处理一个模型消息时，`drainStream(mo.MessageStream)`（`runner_eino.go:304-319`）**阻塞地把整条流吞成一个字符串**，然后才发**一条** `EvtModelCompleted`。流式增量（ADK 以 `schema.StreamReader` 逐块产出）在这一步被丢弃。

前端侧，`AssistantBubble`（`frontend/src/components/Messages/AssistantBubble.tsx`）只在 run 终态（`result_json`/`error_code`）才渲染——这正是"结果最后才弹出"。运行中的实时气泡是 `SystemStatusBubble`（§3 进度行已挂在这）。

§1/§2/§3/§5 四项 follow-up 已在 master 交付；**§4 流式 token 是唯一仍开放的、用户可感知的项**。

## 路线取舍（关键）

**采用 Approach A：走现有 SSE 回放路径发合并的 `model.token` 事件。** 不引入第二传输（B：独立 `/tokens` live 通道，被否——与主 SSE 需保持一致，推翻单一 run-store 哲学）；不逐 chunk 全持久化（C：量最大 + `MAX_EVENTS=500` 头部截断，被否）。

两个已锁定的微决策（spec review 可再翻）：
1. **payload = full-so-far**（非 raw diff）：每个 `model.token` 携带"到目前为止的整段 assistant 文本"。幂等 + 重连回放安全——客户端取**最新**一条 `model.token` 即当前 partial，丢/乱序 chunk 不会 desync。
2. **parent-only gate = 显式 `stream bool` 参数加在 `drain`**（`executeNew`/`executeResume` 传 `true`，`RunChild` 传 `false`）。比从 `TaskContext` 推断 depth 更明确、可单测。

**已知 v1 天花板**：`handleEvents` 以 **1s** ticker（`followPollInterval`，`server.go:22`）轮询 `ListAfter`，所以 delta 按 ~1s 粒度逐出，非 sub-100ms 平滑。v1 可接受（仍远优于"什么都不出到结束"）；平滑推送 = 需专门 push 通道，列 follow-up。

## 改动

### 1. `backend/internal/domain/event.go` — 新事件类型

在 `EvtModelCompleted`（`:18`）旁加：

```go
EvtModelToken    EventType = "model.token"
```

`model.token` 是 `normal` sensitivity、携带**运行中** assistant 全文的事件。

### 2. `backend/internal/runtime/runner_eino.go` — 发 delta（§4 全部）

**(a) `drain` 加 `stream bool` 参数**（`runner_eino.go:246`）：

```go
func (r *EinoRunner) drain(ctx context.Context, runID, checkpointID string,
    iter *adk.AsyncIterator[*adk.AgentEvent], out chan<- RuntimeEvent, stream bool) {
```

- 生产调用点共 3 处传 `true`（父 run 流式）：`executeNew`（`runner_eino.go:217`）+ `executeResume`（`runner_eino.go:241`）。
- 传 `false`（不流式）：`RunChild`（`runner_child.go:124`，子 agent 输出维持折叠成一条 tool 结果）+ 测试桩 `driveDrain`（`runner_failed_tool_test.go:24`，被 3 个 `EvtToolFailed` 测试共用，是 `drain` 的**第 4 个调用点**——改签名后必须补 `false` 否则编译失败；该 helper 不涉及流式）。
- 改 `drain` 签名后，`go build ./...` 会暴露任何漏掉的调用点；上表已枚举全部 4 个（2 父 + 1 子 + 1 测试桩）。
```go
// drainStream 消费流；onDelta 在每 ~200ms 窗口携带"到目前为止全文"flush 一次。
// 仍返回完整字符串（保证 EvtModelCompleted + lastText 不变）。onDelta 可为 nil。
func drainStream(sr *schema.StreamReader[*schema.Message], onDelta func(textSoFar string)) string {
	defer sr.Close()
	var sb strings.Builder
	var lastFlush time.Time
	for {
		chunk, err := sr.Recv()
		if err != nil {
			break
		}
		if chunk != nil {
			sb.WriteString(chunk.Content)
			if onDelta != nil && time.Since(lastFlush) >= 200*time.Millisecond {
				onDelta(sb.String()) // full-so-far
				lastFlush = time.Now()
			}
			if len(sb.String()) > 200000 {
				break
			}
		}
	}
	return sb.String()
}

**实现要点**（planner 必守）：`lastFlush` 初值为零值 `time.Time`，故**首个 chunk 立即 flush**（期望行为，非 bug）；且 coalesce 仅在**收到 chunk 时**判断时间窗——上游流若在中途停顿，停顿期间不产生 token（"~200ms 窗口"是相邻 flush 间隔的**上界**，非严格定时器）。**流结束时不发最后一段 <200ms 的 tail**（无害：`EvtModelCompleted` 恒带全文 + 终态时 UI 切到 `AssistantBubble` 完整结果）——不要"修复"此 tail。`drainStream` 需 `import "time"`（`runner_eino.go` 当前未导入）。

coalesce 用单调时间窗（每 ~200ms 主动流式一次）→ 每个 model 调用产**个位数** `model.token` 事件，而非每 token 一行。窗口常量 `tokenFlushInterval = 200 * time.Millisecond`（包级 var，便于单测替换为小值）。

**(c) `drain` 内接线**（`:282-284`）：

```go
if text == "" && mo.MessageStream != nil {
	var onDelta func(string)
	if stream {
		onDelta = func(txt string) { r.emit(out, runID, domain.EvtModelToken, txt, "", "") }
	}
	text = drainStream(mo.MessageStream, onDelta)
}
```

`stream == false`（子 run）→ `onDelta` 为 nil → 不发 delta，行为同今天。`stream == true` → 每次 coalesce flush 发一条 `model.token`（`full-so-far` 文本在 `RuntimeEvent.Text`）。

**(d) 持久化 + SSE — 零新管线。** `emit`（`runner_eino.go:322-330`）已 `out <- ev` **且** `r.onEvent(ev)`；`runtime.go:35` 把 `OnEvent` 接到 `Events.Append`；`handleEvents` 从 `agent_events` 表 `ListAfter` 回放。故 `model.token` 事件**自动**走 §3 进度行 / 审批事件**同一条**持久化 + SSE 路径。无新 route、无 schema 迁移（`agent_events` 已存任意 `event_type`）。

### 3. 事件契约

`model.token` payload = `{"text": "<到目前为止的完整 assistant 文本>"}`（与 `EvtModelCompleted` 同 shape，full-so-far）。**幂等**：回放客户端取**最新**一条 `model.token` 即当前 partial；终态 `model.completed` 恒携带完整文本。

### 4. `frontend/src` — 累积 + 渲染 live partial

**(a) `lib/types.ts`** — `EventType` union 加 `'model.token'`（该 union 现 ~25 项，`types.ts:20-44`）。

**(b) `lib/status.ts`** — `eventCategory` 把 `model.token` 归 `model` 类（`status.ts:47-77`）；`eventSummary` 加 `model.token` 分支（折叠事件列表里安静显示，如 `"…streaming"`，非终态行）。

**(c) `lib/chat-messages.ts` `emitRun`**（`:113-137`）— 在既有 `if (isActiveRunStatus(run.status))` 块内、与 `progress` 并列派生 live partial（同模式，零新概念）：

```ts
const evs = eventsByRun[run.id] ?? []
...
const tokens = evs.filter((e) => e.event_type === 'model.token')
const partialText = tokens.length
  ? (eventPayload(tokens[tokens.length - 1]).text as string | undefined) // 最新 full-so-far
  : undefined
```

`system-status` `ChatMessage` 变体加可选 `partialText?: string`（`chat-messages.ts:31` 附近），并在 `statusMsg`（`:122-136`）上赋值。取最新一条即当前 partial（full-so-far 幂等，客户端无需 diff 累加）。

**(d) `components/Messages/SystemStatusBubble.tsx`** — 在既有 `progress` 行（`:100-105`）旁渲染 partial：

```tsx
{isActive && partialText && (
  <p className="mt-1 max-h-40 overflow-hidden text-xs whitespace-pre-wrap text-foreground/80">
    {partialText}
  </p>
)}
```

props 加 `partialText?: string`，`ChatMessageList` 把 `message.partialText` 透传。run 终态时 `isActive` 为 false → partial 消失，终态 `AssistantBubble`（`result_json`）接管，无闪烁。

**SSE 重连 / 断点续**：delta 是**已持久化**的合并事件，客户端 run 中途重连 → `ListAfter` 回放 `model.token` 流，最新一条即当前 partial，无需专门 resume 逻辑。

## 数据流

```
ADK 逐块产出 MessageStream
  → drainStream：每 ~200ms 窗口 onDelta(full-so-far)
    → r.emit(out, EvtModelToken, text, ...)  [stream=true 才发]
      → r.onEvent → runtime.Events.Append(agent_events)   [持久化]
        → handleEvents SSE ListAfter 回放（1s 轮询）
          → run-store.events → chat-messages.emitRun 取最新 model.token 文本
            → SystemStatusBubble 渲染 live partial（run 期间）
              → run 终态 → partial 消失，AssistantBubble(result_json) 显示完整结果
```

## 错误 / 边界

| 场景 | 行为 |
|---|---|
| 非流式模型 / `mo.Message != nil` 路径 | `drainStream` 不被调，`onDelta` 不触发 → 无 `model.token` → `partialText` undefined → 只显进度行，结果仍走 `model.completed`。**优雅降级，零回归。** |
| `MAX_EVENTS=500` 头部截断 | 只丢**早期** partial；最新 `model.token` 仍是当前 partial。可接受（同 §3 计数低估）。 |
| 流中途 run 失败/取消/超时 | partial 是瞬态 UI；终态 `AssistantBubble`（error_code / result_json）显示结果，无孤儿 partial。 |
| `stream=false`（子 run / `RunChild`） | `onDelta` nil → 不发 delta，子输出仍折叠成一条 `tool.completed`，**行为同今天**。 |
| 重连中途 | 已持久化 → `ListAfter` 续播，最新 token = 当前 partial。 |

## 测试

- **`runner_eino`（Go）**：`drainStream` coalescer 单测——喂一个假 `StreamReader`（N 块、间隔 > 窗口，窗口常量为 var 可注入小值）→ 断言 `onDelta` 触发次数 ≈ 窗口数、每次为**递增 full-so-far**、最终返回值 == 拼接全文（证明 `EvtModelCompleted`/`lastText` 不变）；`onDelta==nil` 时零触发。
- **`drain` 门控**：`stream=true` → `out` 通道收到 `EvtModelToken`；`stream=false` → 收不到（保子 run 不变）。
- **前端 `chat-messages.test.ts`**：active run + 末尾 `model.token` → `statusMsg.partialText` == 最新 text；terminal run → 无 `partialText`；无 `model.token`（非流式）→ `partialText` undefined。
- **前端 `SystemStatusBubble.test.tsx`**：`isActive && partialText` → 渲染 partial 行；终态 / 无 partial → 不渲染。
- 全量 `go test ./... -run 'Stream|Drain'` + `npx tsc --noEmit` + `npx vitest run` 绿。

## 验收

- 父 run 运行中，`SystemStatusBubble` 实时显示流式 partial（~1s 粒度逐出），终态切换为 `AssistantBubble` 完整结果。
- 子 agent（task 工具）输出**不**实时串（仍折叠）——`RunChild` 路径 `stream=false`。
- 非流式模型 / 失败 / 重连 场景**零回归**（无 `model.token` 时 UI 与今天一致）。
- 无新 route / 无 schema 迁移 / 无第二传输；`git diff` 不碰子 run 行为与终态渲染。

## 明确不做（YAGNI / follow-up）

- **sub-100ms 平滑推送**（需专门 push 通道，改 `handleEvents` 的 1s 轮询为真 SSE 推送）— 列 follow-up。
- **子 run / 子 agent 的 token 流式**（价值有限，子输出本就折叠）— 列 follow-up。
- **跨 run / 刷新后仍精确的 token 对齐**（依赖后端 `budget.consumed`，§3 已留 follow-up）。
