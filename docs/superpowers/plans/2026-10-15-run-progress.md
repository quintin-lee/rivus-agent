# 运行进度可视化 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** running run 的状态气泡实时显示一行"工具 x/y · 模型 m/n · Ns"（工具/模型调用计数 + 已运行时长）；终态 run 不显示。纯前端派生，零后端改动。

**Architecture:** 在 `chat-messages.ts` 的 `system-status` 变体加可选 `progress` 字段，`emitRun` 里仅当 `isActiveRunStatus(run.status)` 时计算：工具/模型计数来自该 run 的 `tool.completed`/`model.completed` 事件（`eventsByRun[run.id]`），**时长来自 `Run` 对象时间戳 + 注入的 `now`**（`elapsed = now - (run.started_at ?? run.created_at)`），而非事件流 `created_at`——因为 SSE wire 与 poll 两条路径都把事件 `created_at` 硬编码为 0（`sse-client.ts` `parseBlock`、`api.ts` `parseSSE`），用事件算时长恒为 0s。`now` 由 `ChatMessageList` 的 useMemo 调用点注入 `Date.now()`，保持 `deriveMessages` 纯函数可测。`SystemStatusBubble` 加 `progress` prop + 一行渲染。

**Tech Stack:** React/Vite/Zustand（`frontend/`），`npx tsc --noEmit` + `npx vitest run`。

**Spec:** `docs/superpowers/specs/2026-10-15-run-progress-design.md`

**Out of scope（明确不做）:** 后端事件（`budget.consumed`）/SSE 协议；iteration/子步骤细分；节流防抖；终态 run 的进度行。

---

## File Structure

- Modify: `frontend/src/lib/chat-messages.ts` — `DeriveInput` 加 `now: number`；`system-status` 变体加 `progress?`；`emitRun` 加 5 参 `now` 并计算；`isActiveRunStatus` import；两处 `emitRun` 调用点传 `now`。
- Modify: `frontend/src/components/Messages/SystemStatusBubble.tsx` — `Props` 加 `progress?`；`isActive && progress` 时渲染一行。
- Modify: `frontend/src/components/ChatMessageList.tsx` — useMemo 传 `now: Date.now()`；`system-status` 分支传 `progress={message.progress}`。
- Test: `frontend/src/lib/chat-messages.test.ts`（`mkEv`/`mkRun` 已存在，扩展 `deriveMessages` 调用加 `now`）；`frontend/src/components/Messages/SystemStatusBubble.test.tsx`。

## Chunk 1: 派生逻辑

### Task 1: `chat-messages.ts` 进度派生

**Files:**
- Modify: `frontend/src/lib/chat-messages.ts`、`frontend/src/components/ChatMessageList.tsx`（仅 `now` 注入；`progress` 接线在 Chunk 2）

- [ ] **Step 1: 写失败测试**

`chat-messages.test.ts` 现有 helper：`mkRun(id, status, extra)`（`budget` 默认 `max_tool_calls:10/max_model_calls:10`、`created_at:1`）、`mkEv(seq, type, payload)`（`created_at:0`）、`deriveMessages({runHistory, activeRun, activeSteps, eventsByRun, pendingGoals})`。

加一个用例（`describe('deriveMessages')` 内）：

```ts
it('derives progress (tool/model counts + elapsed) for an active run', () => {
  const startMs = 1_000_000
  const now = startMs + 2000 // 2s 后
  const run = mkRun('r1', 'running', { started_at: startMs })
  const evs = [
    mkEv(1, 'tool.completed'),
    mkEv(2, 'tool.completed'),
    mkEv(3, 'model.completed'),
    mkEv(4, 'model.completed'),
    mkEv(5, 'model.completed'),
  ]
  const out = deriveMessages({
    runHistory: [],
    activeRun: run,
    activeSteps: [],
    eventsByRun: { r1: evs },
    pendingGoals: [],
    now,
  })
  const status = out.find((m) => m.kind === 'system-status')
  expect(status).toMatchObject({
    progress: { toolCalls: 2, modelCalls: 3, seconds: 2 },
  })
})

it('does not derive progress for a terminal run', () => {
  const run = mkRun('r1', 'succeeded', { started_at: 1 })
  const out = deriveMessages({
    runHistory: [run],
    activeRun: null,
    activeSteps: [],
    eventsByRun: { r1: [mkEv(1, 'tool.completed')] },
    pendingGoals: [],
    now: 1000,
  })
  const status = out.find((m) => m.kind === 'system-status')
  expect((status as any).progress).toBeUndefined()
})

it('falls back to created_at when started_at is missing (active run)', () => {
  // mkRun 默认 created_at:1、无 started_at → startMs = created_at = 1
  const run = mkRun('r1', 'running')
  const out = deriveMessages({
    runHistory: [],
    activeRun: run,
    activeSteps: [],
    eventsByRun: { r1: [mkEv(1, 'tool.completed')] },
    pendingGoals: [],
    now: 5001, // 1 + 5000ms = 5s
  })
  const status = out.find((m) => m.kind === 'system-status')
  expect((status as any).progress).toMatchObject({ toolCalls: 1, seconds: 5 })
})
```

注意：现有所有 `deriveMessages({ ... })` 测试调用（约 14 处）需补 `now` 字段（mechanical：传任意 `number`，如 `now: 0` 或合适的 ms）。**同时**唯一的生产调用点 `ChatMessageList.tsx` 的 useMemo（`deriveMessages({ … })`）也必须在本 Chunk 内补 `now: Date.now()`——因为 `DeriveInput.now` 是必需字段，缺了它 Chunk 1 的 `npx tsc --noEmit` 会报 "Property 'now' is missing"，Chunk 1 的提交就带红。`progress` 的渲染接线仍留 Chunk 2。先加新用例，`tsc`/`vitest` 会因 `now` 未声明而报错——这就是失败态。

- [ ] **Step 2: 运行确认失败**

Run: `cd frontend && npx vitest run src/lib/chat-messages.test.ts`
Expected: FAIL —— 类型层 `DeriveInput` 无 `now`、`progress` 未定义，断言不过。

- [ ] **Step 3: 实现**

`chat-messages.ts`：

1. import：第 2 行现为 `import { TERMINAL_RUN_STATUSES } from './status'`，改为
   `import { TERMINAL_RUN_STATUSES, isActiveRunStatus } from './status'`。
2. `DeriveInput`（line 48）加字段：
   ```ts
   export interface DeriveInput {
     runHistory: Run[]
     activeRun: Run | null
     activeSteps: Step[]
     eventsByRun: Record<string, AgentEvent[]>
     pendingGoals: PendingGoal[]
     now: number
   }
   ```
3. `deriveMessages`（line 61-62）解构加 `now`：
   `const { runHistory, activeRun, activeSteps, eventsByRun, pendingGoals, now } = input`
   两处 `emitRun` 调用（line 85、88）传 `now`：
   `emitRun(messages, run, activeSteps, eventsByRun, now)` / `emitRun(messages, activeRun, activeSteps, eventsByRun, now)`
4. `system-status` 变体（line 22-30）加可选字段：
   ```ts
   | {
       kind: 'system-status'
       ...
       error?: { code: string; summary?: string }
       progress?: { toolCalls: number; modelCalls: number; seconds: number }
     }
   ```
5. `emitRun` 签名（line 94）加第 5 参 `now: number`；在 `messages.push(statusMsg)`（line 124）**之后**、`const events = …` 附近加：
   ```ts
   if (isActiveRunStatus(run.status)) {
     const evs = eventsByRun[run.id] ?? []
     const toolCalls = evs.filter((e) => e.event_type === 'tool.completed').length
     const modelCalls = evs.filter((e) => e.event_type === 'model.completed').length
     const startMs = run.started_at ?? run.created_at
     const seconds = startMs > 0 ? Math.max(0, Math.round((now - startMs) / 1000)) : 0
     ;(statusMsg as any).progress = { toolCalls, modelCalls, seconds }
   }
   ```
   （`statusMsg` 是 `ChatMessage`；用 `as any` 或声明为带 `progress` 的具体 union 成员后直接赋值——以更整洁的类型为准，优先给局部变量 `const statusMsg: ChatMessage = {…}` 后单独 `statusMsg.progress = …`，TS 会因 union 收窄报错，故实现时用 `as { progress?: … }` 断言或重构该局部为 `typeof statusMsg & { progress?: … }`。任选其一，以 `tsc` 通过为准。）
6. **生产调用点（本 Chunk 内完成，勿留到 Chunk 2）**：`frontend/src/components/ChatMessageList.tsx` 的 useMemo（line 22-24，`deriveMessages({ … })`）加 `now: Date.now()` 字段。不要把 `Date.now()` 放进 useMemo 依赖数组（否则每次 render 重算、破坏 memo）；`now` 仅用于派生时长，随事件 append 触发重算已足够。


- [ ] **Step 4: 运行确认通过**

Run: `cd frontend && npx vitest run src/lib/chat-messages.test.ts && npx tsc --noEmit`
Expected: PASS + 类型绿。

- [ ] **Step 5: Commit**

```bash
git add frontend/src/lib/chat-messages.ts frontend/src/lib/chat-messages.test.ts frontend/src/components/ChatMessageList.tsx
git commit -m "feat(frontend): derive live progress (tool/model counts + elapsed) in system-status"
```

## Chunk 2: 渲染

### Task 2: `SystemStatusBubble` + `ChatMessageList`

**Files:**
- Modify: `frontend/src/components/Messages/SystemStatusBubble.tsx`、`frontend/src/components/ChatMessageList.tsx`

- [ ] **Step 1: 写失败测试**

`SystemStatusBubble.test.tsx` 现有 `run(status)` helper（`budget: {max_model_calls:10, max_tool_calls:10, …}`）+ `render(<SystemStatusBubble run={…} onRetryRun={…} />)`。加：

```ts
it('renders a live progress row for an active run with progress', () => {
  const r = { ...run('running'), started_at: 1 }
  render(
    <SystemStatusBubble
      run={r}
      onRetryRun={vi.fn()}
      progress={{ toolCalls: 2, modelCalls: 3, seconds: 2 }}
    />,
  )
  expect(screen.getByText(/工具 2\/10/)).toBeTruthy()
  expect(screen.getByText(/模型 3\/10/)).toBeTruthy()
  expect(screen.getByText(/2s/)).toBeTruthy()
})

it('renders no progress row when progress is absent (terminal)', () => {
  const r = run('succeeded')
  render(<SystemStatusBubble run={r} onRetryRun={vi.fn()} />)
  expect(screen.queryByText(/工具 \d+\/\d+/)).toBeNull()
})
```

- [ ] **Step 2: 运行确认失败**

Run: `cd frontend && npx vitest run src/components/Messages/SystemStatusBubble.test.tsx`
Expected: FAIL —— `SystemStatusBubble` 未接受 `progress`。

- [ ] **Step 3: 实现 `SystemStatusBubble`**

`SystemStatusBubble.tsx`：`Props`（line 8）加 `progress?: { toolCalls: number; modelCalls: number; seconds: number }`；函数参数（line 14）解构 `progress`。`isActive` 已在 line 17（`!TERMINAL_RUN_STATUSES.includes(run.status)`）。在 goal 行（line 98 `<p className="mt-2 text-xs …">{run.goal}</p>`）之后、`isActive && progress` 时加：

```tsx
{isActive && progress && run.budget && (
  <p className="mt-1 text-xs text-muted-foreground">
    工具 {progress.toolCalls}/{run.budget.max_tool_calls} ·{' '}
    模型 {progress.modelCalls}/{run.budget.max_model_calls} ·{' '}
    {progress.seconds}s
  </p>
)}
```

- [ ] **Step 4: `ChatMessageList` 补传 progress（now 已在 Chunk 1 注入）**

`ChatMessageList.tsx` 的 `system-status` 分支（line 70 `<SystemStatusBubble`）加 `progress={message.progress}`。
（`now: Date.now()` 已在 Chunk 1 Task 1 Step 3 的 useMemo 注入，本步只补 progress 接线。）

- [ ] **Step 5: 运行确认通过**

Run: `cd frontend && npx vitest run src/components/Messages/SystemStatusBubble.test.tsx && npx tsc --noEmit`
Expected: PASS + 类型绿。

- [ ] **Step 6: Commit**

```bash
git add frontend/src/components/Messages/SystemStatusBubble.tsx frontend/src/components/Messages/SystemStatusBubble.test.tsx frontend/src/components/ChatMessageList.tsx
git commit -m "feat(frontend): render live progress row in SystemStatusBubble"
```

## Chunk 3: Acceptance

- [ ] **Step 1: 前端全量**

Run: `cd frontend && npx tsc --noEmit && npx vitest run && npx vite build`
Expected: 全绿。

- [ ] **Step 2: 纯前端验收**

Run: `git diff --name-only`
Expected: 不含 `backend/`（本 spec 纯前端）。

- [ ] **Step 3: 行为验收对照**

- running 气泡实时显示"工具 x/y · 模型 m/n · Ns"，随事件 append 刷新；时长来自 `run.started_at`/`created_at` + 注入 `now`（**非**事件 `created_at`，后者 live 恒 0）。
- 终态 run 不显示进度行（`isActive` 守卫）。
- 刷新后：`activateRun` 清 `events`、SSE 以 `after_seq=0` 重连 → 后端全量重放 → 计数/时长恢复。
