# 运行进度可视化 — 设计文档

- **日期**: 2026-10-15
- **范围**: 纯前端。`frontend/src/lib/chat-messages.ts`（进度派生）、`frontend/src/components/Messages/SystemStatusBubble.tsx`（渲染一行）+ 测试。后端零改动。
- **状态**: 设计已确认。
- **上级 spec**: 兑现 `2026-10-14-follow-ups.md` §3，但**采用比原提议更省的路线**（见下"路线取舍"）。

## 路线取舍（关键）

follow-up §3 原提议是"新增 `budget.consumed` 后端事件"。本 spec **不新增后端事件**，改为**纯前端派生**：

- 工具/模型调用数：`tool.completed` / `model.completed` 事件**本就已实时流经 SSE**（上轮确认），前端 `run-store.events` 已按 run 分组（`groupEventsByRun`）。
- 预算上限：`run.budget` 已在 run 对象里（`types.ts` `Budget`：`max_model_calls`/`max_tool_calls` 等）。
- 已运行时长：`run` 对象自带真实时间戳（`types.ts` `Run.created_at` / `started_at?`，后端 `nowMs()` 填充）；`elapsed = now - (run.started_at ?? run.created_at)`，`now` 由调用点注入（`ChatMessageList` useMemo 传 `Date.now()`，保持 `deriveMessages` 纯函数可测）。**不**用事件流 `created_at` 算时长——SSE wire 与 poll 两条路径都硬编码 `created_at: 0`（`sse-client.ts:31` `parseBlock`、`api.ts` `parseSSE`），事件流算时长恒为 0s。

三者一乘除即得"工具 {tool}/{max_tool} · 模型 {model}/{max_model} · {N}s"。纯前端、零后端、不引入事件协议变更，YAGNI。若后续要跨 run/刷新后仍精确，再上后端 `budget.consumed`（留 follow-up）。

## 改动

### 1. `frontend/src/lib/chat-messages.ts` — 进度派生

`system-status` `ChatMessage` 变体加可选字段：

```ts
| {
    kind: 'system-status'
    ...
    progress?: { toolCalls: number; modelCalls: number; seconds: number }
  }
```

`emitRun` 里，**仅当 run 非终态**（`isActiveRunStatus(run.status)`；`isActiveRunStatus` 需从 `status.ts` import——现 `chat-messages.ts` 只 import 了 `TERMINAL_RUN_STATUSES`）时，算：

```ts
if (isActiveRunStatus(run.status)) {
  const evs = eventsByRun[run.id] ?? []
  const toolCalls = evs.filter((e) => e.event_type === 'tool.completed').length
  const modelCalls = evs.filter((e) => e.event_type === 'model.completed').length
  const startMs = run.started_at ?? run.created_at
  const seconds = startMs > 0 ? Math.max(0, Math.round((now - startMs) / 1000)) : 0
  statusMsg.progress = { toolCalls, modelCalls, seconds }
}
```

（工具/模型计数来自事件流 `*.completed`；时长来自 run 时间戳 + 注入的 `now`。`DeriveInput` 新增 `now: number`（`emitRun` 经 `deriveMessages` 拿到）。事件 append 天然驱动重算；`MAX_EVENTS=500` 头部丢弃时计数可能低估——实时 gauge 可接受，不追求对齐预算上限。）

### 2. `frontend/src/components/Messages/SystemStatusBubble.tsx` — 渲染一行

`Props` 加可选 `progress`；仅在 `isActive && progress` 时，在气泡里（goal 行附近）加一行：

```tsx
{isActive && progress && run.budget && (
  <p className="mt-1 text-xs text-muted-foreground">
    工具 {progress.toolCalls}/{run.budget.max_tool_calls} ·
    模型 {progress.modelCalls}/{run.budget.max_model_calls} ·
    {progress.seconds}s
  </p>
)}
```

（`run.budget` 恒在 run 对象里；`progress` 仅 running 时有。终态 run 不显示进度行。）

### 3. `ChatMessageList.tsx` — 传参 + 注入 now

`deriveMessages` 的 useMemo 调用点（`ChatMessageList.tsx:70-74`，唯一非测试调用方）传 `now: Date.now()`；`system-status` 分支补传 `progress={message.progress}` 给 `SystemStatusBubble`。

## 明确不做（YAGNI）
- 不新增后端事件、不改后端、不改 SSE 协议。
- 不做 iteration/子步骤细分（只 tool/model 计数 + 秒数）。
- 不做节流/防抖：进度随事件 append 自然更新，`system-status` 气泡本就是每事件重算 `deriveMessages`（memo 已覆盖），量级小（`MAX_EVENTS` 有界），无性能问题。
- 终态 run 不显示进度（`isActive` 守卫）。

## 数据流

```
SSE 事件 append → run-store.events
  → deriveMessages.emitRun: running run 从 eventsByRun[run.id] 算 progress(tool/model/seconds)
  → ChatMessageList → SystemStatusBubble(progress) 渲染 "工具 x/y · 模型 m/n · Ns" 行
  → 下一个事件到达 → events 更新 → 重算 → 行刷新
```

## 错误 / 边界

| 场景 | 行为 |
|---|---|
| 无事件（刚 submit） | 计数 0；时长从 `run.started_at`/`created_at` 起算（≥0），行显示 0/0 + 实际秒数，正常 |
| 事件数 > MAX_EVENTS（500） | `events` 头部丢弃，tool/model 计数可能低估（实时 gauge 可接受，非 bug） |
| 终态 run | `isActive` 守卫：不派生/不渲染 progress 行 |
| `budget` 缺字段 | `run.budget` 恒有（后端 `domain.Budget` 必填）；渲染用 `max_*`，缺失不会崩 |

## 测试

- `chat-messages.test.ts`：
  - running run + 注入 `now`（如 `run.started_at = startMs`，`now = startMs + 2000`）+ 事件流（`tool.completed`×2、`model.completed`×3）→ `system-status.progress = {toolCalls:2, modelCalls:3, seconds:2}`；
  - `run.started_at` 缺省时回退 `created_at`（一条用例即可）；
  - 终态 run → `system-status` 无 `progress` 字段。
- `SystemStatusBubble.test.tsx`：
  - 传入 running run + `progress` → 渲染出 `工具 2/10 · 模型 3/10 · 2s` 文案；
  - 终态 run + 无 progress → 不渲染进度行。
- 全量 `npx tsc --noEmit` + `npx vitest run` 绿。

## 验收

- running 气泡实时显示"工具 x/y · 模型 m/n · Ns"，随事件更新；终态不显示。
- 纯前端：`git diff` 不含 `backend/`。
- 前端测试全绿。
