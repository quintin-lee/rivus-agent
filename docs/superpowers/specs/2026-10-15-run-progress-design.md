# 运行进度可视化 — 设计文档

- **日期**: 2026-10-15
- **范围**: 纯前端。`frontend/src/lib/chat-messages.ts`（进度派生）、`frontend/src/components/Messages/SystemStatusBubble.tsx`（渲染一行）+ 测试。后端零改动。
- **状态**: 设计已确认。
- **上级 spec**: 兑现 `2026-10-14-follow-ups.md` §3，但**采用比原提议更省的路线**（见下"路线取舍"）。

## 路线取舍（关键）

follow-up §3 原提议是"新增 `budget.consumed` 后端事件"。本 spec **不新增后端事件**，改为**纯前端派生**：

- 工具/模型调用数：`tool.completed` / `model.completed` 事件**本就已实时流经 SSE**（上轮确认），前端 `run-store.events` 已按 run 分组（`groupEventsByRun`）。
- 预算上限：`run.budget` 已在 run 对象里（`types.ts` `Budget`：`max_model_calls`/`max_tool_calls` 等）。
- 已运行时长：事件流 `created_at` 的 `max - min`（事件有 `created_at` ms）。

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

`emitRun` 里，**仅当 run 非终态**（`isActiveRunStatus(run.status)`，已 import）时，从该 run 的事件算：

```ts
if (isActiveRunStatus(run.status)) {
  const evs = eventsByRun[run.id] ?? []
  const startedAt = evs.length ? evs[0].created_at : 0
  const now = evs.length ? evs[evs.length - 1].created_at : 0
  const progress = {
    toolCalls: evs.filter((e) => e.event_type === 'tool.completed').length,
    modelCalls: evs.filter((e) => e.event_type === 'model.completed').length,
    seconds: startedAt > 0 ? Math.round((now - startedAt) / 1000) : 0,
  }
  statusMsg.progress = progress
}
```

（事件在 store 里按 seq 升序 append，`created_at` 单调不减，首尾取 min/max 成立；为空则 seconds=0。）

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

### 3. `ChatMessageList.tsx` — 传参

`Bubble` 的 `system-status` 分支已把 `message` 相关字段传给 `SystemStatusBubble`，补传 `progress={message.progress}`。

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
| 无事件（刚 submit） | `progress.seconds=0`、计数 0，行显示 0/0，正常 |
| 事件流空 `created_at` | seconds 走 `startedAt>0` 守卫为 0 |
| 终态 run | 不派生/不渲染 progress 行 |
| `budget` 缺字段 | `run.budget` 恒有（后端 `domain.Budget` 必填）；渲染用 `run.budget.max_*`，缺失显示 undefined 不会崩（可选链非必需，budget 保证存在） |

## 测试

- `chat-messages.test.ts`：
  - running run + 事件流（`tool.completed`×2、`model.completed`×3，`created_at` 0/1000/2000）→ `system-status.progress = {toolCalls:2, modelCalls:3, seconds:2}`。
  - 终态 run → `system-status` 无 `progress` 字段。
- `SystemStatusBubble.test.tsx`：
  - 传入 running run + `progress` → 渲染出 `工具 2/10 · 模型 3/10 · 2s` 文案；
  - 终态 run + 无 progress → 不渲染进度行。
- 全量 `npx tsc --noEmit` + `npx vitest run` 绿。

## 验收

- running 气泡实时显示"工具 x/y · 模型 m/n · Ns"，随事件更新；终态不显示。
- 纯前端：`git diff` 不含 `backend/`。
- 前端测试全绿。
