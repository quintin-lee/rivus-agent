# Frontend UX Polish Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the frontend-only UX polish round: Stop button, approval collapse rendering, event-text fallbacks aligned to real payload keys, empty-session placeholder, cancelled-run bubble.

**Architecture:** All changes are in-place on existing React/Vite/Zustand files. One store addition (`run-store.cancelActiveRun` + `cancelling`/`cancelError` fields) drives the Stop button; everything else reuses the existing SSE → store → `deriveMessages` → bubble pipeline. No new source files except one test file (`lib/status.test.ts`).

**Tech Stack:** React 18, Zustand, Vitest + Testing Library, TypeScript. Test runner: `npx vitest run` (workdir `frontend/`); typecheck: `npx tsc --noEmit`.

**Spec:** `docs/superpowers/specs/2026-10-14-frontend-ux-polish-design.md`
**Follow-ups (out of scope here):** `docs/superpowers/specs/2026-10-14-follow-ups.md`

---

## File Structure

- Modify: `frontend/src/store/run-store.ts` — add `cancelling`, `cancelError`, `cancelActiveRun`; reset both fields in `activateRun` and `loadRunHistory`.
- Modify: `frontend/src/store/run-store.test.ts` — mock `api.cancelRun`, reset new fields in `beforeEach`, 3 new store tests.
- Modify: `frontend/src/components/ChatInputBar.tsx` — Send button becomes Stop when the active run is non-terminal; inline `cancelError` line.
- Modify: `frontend/src/components/ChatInputBar.test.tsx` — 5 new tests.
- Modify: `frontend/src/lib/status.ts` — `eventSummary` tool/model branches read `p.tool`; static model text; `Unknown event` default.
- Create: `frontend/src/lib/status.test.ts` — new test file for `eventSummary`.
- Modify: `frontend/src/components/ChatMessageList.tsx` — empty placeholder.
- Modify: `frontend/src/components/ChatMessageList.test.tsx` — 1 new test.
- Modify: `frontend/src/components/Messages/AssistantBubble.tsx` — `error_code === 'cancelled'` branch.
- Modify: `frontend/src/components/Messages/AssistantBubble.test.tsx` — 2 new tests.
- Modify: `frontend/src/lib/chat-messages.ts` — `system-approval` state union + `'expired'`; `deriveApprovals(runId, events, runTerminal)`.
- Modify: `frontend/src/lib/chat-messages.test.ts` — 3 new tests.
- Modify: `frontend/src/components/ApprovalCard.tsx` — resolved states render a compact single line.
- Modify: `frontend/src/components/ApprovalCard.test.tsx` — 1 new test.

---

## Chunk 1: Stop button (store + input bar)

### Task 1: `run-store` — `cancelActiveRun`

**Files:**
- Modify: `frontend/src/store/run-store.ts`
- Test: `frontend/src/store/run-store.test.ts`

**Context:** `RunState` (run-store.ts:15-31) has no cancel action; `api.cancelRun` exists (`lib/api.ts:147`). `TERMINAL` refresh is already wired (`appendEvent` → `REFRESH_EVENTS` → `fetchRunAndSteps`), so after a successful cancel the existing pipeline turns the run terminal and the Stop button disappears.

- [ ] **Step 1: Add failing store tests**

In `run-store.test.ts`, extend the mock factory (add `cancelRun`), add the two fields to the `beforeEach` reset, and add three tests:

```ts
vi.mock('@/lib/api', () => ({
  api: {
    getRun: vi.fn().mockResolvedValue({ run: {}, steps: [] }),
    listRuns: vi.fn().mockResolvedValue([]),
    createRun: vi.fn().mockResolvedValue({ run_id: 'r1', status: 'queued', duplicated: false }),
    cancelRun: vi.fn().mockResolvedValue(undefined),
  },
}))
```

`beforeEach` additions:

```ts
useRunStore.setState({
  activeRunId: null,
  activeRun: null,
  activeSteps: [],
  events: [],
  eventFilter: 'all',
  runHistory: [],
  pendingGoals: [],
  cancelling: false,
  cancelError: null,
})
```

New tests (inside the existing `describe('run-store')`):

```ts
it('cancelActiveRun: calls api.cancelRun and clears cancelling/cancelError on success', async () => {
  useRunStore.setState({ activeRunId: 'r1', cancelling: true, cancelError: 'old' })
  await useRunStore.getState().cancelActiveRun('r1')
  expect(vi.mocked(api.cancelRun)).toHaveBeenCalledWith('r1')
  expect(useRunStore.getState().cancelling).toBe(false)
  expect(useRunStore.getState().cancelError).toBe(null)
})

it('cancelActiveRun: stores the error message and re-enables on failure', async () => {
  vi.mocked(api.cancelRun).mockRejectedValueOnce(new Error('stop failed'))
  await useRunStore.getState().cancelActiveRun('r1')
  expect(useRunStore.getState().cancelling).toBe(false)
  expect(useRunStore.getState().cancelError).toBe('stop failed')
})

it('activateRun resets cancelling and cancelError', async () => {
  vi.mocked(api.getRun).mockResolvedValueOnce({ run: {}, steps: [] } as never)
  useRunStore.setState({ cancelling: true, cancelError: 'old' })
  await useRunStore.getState().activateRun('r2')
  expect(useRunStore.getState().cancelling).toBe(false)
  expect(useRunStore.getState().cancelError).toBe(null)
})
```

- [ ] **Step 2: Run tests, confirm they fail**

Run (workdir `frontend/`): `npx vitest run src/store/run-store.test.ts`
Expected: FAIL — `cancelActiveRun is not a function` / type errors.

- [ ] **Step 3: Implement the store fields + action**

In `run-store.ts`:

1. `RunState` interface (lines 15-31) — add to the state and action declarations:

```ts
cancelling: boolean
cancelError: string | null
cancelActiveRun: (id: string) => Promise<void>
```

2. Initial state block (lines 50-56) — add:

```ts
cancelling: false,
cancelError: null,
```

3. `activateRun` (line 58) — include the reset in the `set`:

```ts
activateRun: async (id) => {
  set({ activeRunId: id, events: [], cancelling: false, cancelError: null })
  await get().fetchRunAndSteps(id)
},
```

4. `loadRunHistory` (lines 86-89) — reset on session change:

```ts
loadRunHistory: async (sessionId) => {
  const runs = await api.listRuns(sessionId)
  set({ runHistory: runs, pendingGoals: [], cancelling: false, cancelError: null })
},
```

5. Add the action after `submitRun`:

```ts
cancelActiveRun: async (id) => {
  set({ cancelling: true, cancelError: null })
  try {
    await api.cancelRun(id)
    set({ cancelling: false, cancelError: null })
  } catch (e) {
    set({ cancelling: false, cancelError: e instanceof Error ? e.message : 'Stop failed' })
  }
},
```

- [ ] **Step 4: Run tests, confirm they pass**

Run (workdir `frontend/`): `npx vitest run src/store/run-store.test.ts`
Expected: all PASS (including the pre-existing tests).

- [ ] **Step 5: Commit**

```bash
git add frontend/src/store/run-store.ts frontend/src/store/run-store.test.ts
git commit -m "feat(run-store): add cancelActiveRun with cancelling/cancelError state"
```

### Task 2: `ChatInputBar` — Stop button

**Files:**
- Modify: `frontend/src/components/ChatInputBar.tsx`
- Test: `frontend/src/components/ChatInputBar.test.tsx`

**Context:** `isActiveRunStatus` is exported from `lib/status.ts:42`. The existing test file already mocks `api` (with `cancelRun`) and seeds stores via `seedStores`. The Stop button replaces Send **in place** when the active run is non-terminal; while cancelling the button reads "Stopping…".

- [ ] **Step 1: Add failing component tests**

Append to `ChatInputBar.test.tsx`. The file currently has **no** `@/lib/types` or `@/lib/api` import, so add both to the import block at the top of the file:

```tsx
import { api } from '@/lib/api'
import type { Run } from '@/lib/types'
```

Also extend the `seedStores` helper so every test starts from a clean cancel state (prevents inter-test leaking of `cancelling`/`cancelError`): add `cancelling: false, cancelError: null` to its `useRunStore.setState({ … })` call.

```tsx
function runningRun(status: Run['status'] = 'running'): Run {
  return {
    id: 'r1',
    session_id: 's1',
    owner_id: 'o1',
    status,
    mode: 'react',
    goal: 'do thing',
    budget: {
      max_duration_seconds: 60,
      max_model_calls: 10,
      max_tool_calls: 10,
      max_iterations: 5,
      max_output_bytes: 1024,
    },
    checkpoint_id: '',
    attempt: 1,
    created_at: 1,
    updated_at: 1,
  }
}

describe('ChatInputBar Stop', () => {
  it('shows Stop instead of Send while the active run is in progress', () => {
    seedStores()
    useRunStore.setState({ activeRun: runningRun() })
    render(<ChatInputBar />)
    expect(screen.getByRole('button', { name: /stop/i })).toBeTruthy()
    expect(screen.queryByRole('button', { name: /send/i })).toBeNull()
  })

  it('clicking Stop calls api.cancelRun with the active run id', async () => {
    seedStores()
    useRunStore.setState({ activeRun: runningRun() })
    render(<ChatInputBar />)
    fireEvent.click(screen.getByRole('button', { name: /stop/i }))
    await new Promise((r) => setTimeout(r, 0))
    expect(api.cancelRun).toHaveBeenCalledWith('r1')
  })

  it('disables the Stop button while cancelling', () => {
    seedStores()
    useRunStore.setState({ activeRun: runningRun(), cancelling: true })
    render(<ChatInputBar />)
    expect((screen.getByRole('button', { name: /stopping/i }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('renders cancelError inline', () => {
    seedStores()
    useRunStore.setState({ activeRun: runningRun(), cancelError: 'Stop failed' })
    render(<ChatInputBar />)
    expect(screen.getByText(/stop failed/i)).toBeTruthy()
  })

  it('shows Send again when the active run is terminal', () => {
    seedStores()
    useRunStore.setState({ activeRun: runningRun('succeeded') })
    render(<ChatInputBar />)
    expect(screen.getByRole('button', { name: /send/i })).toBeTruthy()
    expect(screen.queryByRole('button', { name: /stop/i })).toBeNull()
  })
})
```

Note: each new test calls `seedStores()` (which resets `activeRun: null`, `cancelling: false`, `cancelError: null`) and then a per-test `useRunStore.setState` that overrides what it needs.

- [ ] **Step 2: Run tests, confirm they fail**

Run (workdir `frontend/`): `npx vitest run src/components/ChatInputBar.test.tsx`
Expected: the 5 new tests FAIL (Stop button not found); existing 4 tests still PASS.

- [ ] **Step 3: Implement the Stop button**

In `ChatInputBar.tsx`:

1. Imports: add

```ts
import { isActiveRunStatus } from '@/lib/status'
```

Only `isActiveRunStatus` is needed; the cancel goes through the store action.

2. Store reads (after line 9):

```ts
const activeRun = useRunStore((s) => s.activeRun)
const cancelling = useRunStore((s) => s.cancelling)
const cancelError = useRunStore((s) => s.cancelError)
const cancelActiveRun = useRunStore((s) => s.cancelActiveRun)
```

3. Derived flag (after `busy`):

```ts
const stopping = activeRun !== null && isActiveRunStatus(activeRun.status)
```

4. Replace the single Send button (lines 54-61) with a conditional pair:

```tsx
{stopping ? (
  <button
    type="button"
    className="rounded-lg bg-rose-600 px-4 py-2.5 text-sm font-medium text-white hover:bg-rose-700 disabled:opacity-50"
    disabled={busy || cancelling}
    onClick={() => activeRun && void cancelActiveRun(activeRun.id)}
  >
    {cancelling ? 'Stopping…' : '⏹ Stop'}
  </button>
) : (
  <button
    type="button"
    className="rounded-lg bg-primary px-4 py-2.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
    disabled={busy || !goal.trim()}
    onClick={submit}
  >
    Send
  </button>
)}
```

5. After the button row (before `<NewRunDialog …/>`), add the inline error:

```tsx
{cancelError && (
  <p className="mt-2 text-xs text-rose-600 dark:text-rose-400">{cancelError}</p>
)}
```

- [ ] **Step 4: Run tests, confirm they pass**

Run (workdir `frontend/`): `npx vitest run src/components/ChatInputBar.test.tsx`
Expected: 9 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/ChatInputBar.tsx frontend/src/components/ChatInputBar.test.tsx
git commit -m "feat(chat-input-bar): Stop button replaces Send while a run is active"
```

---

## Chunk 2: Event texts, empty state, cancelled bubble

### Task 3: `eventSummary` — real payload keys + unknown fallback

**Files:**
- Modify: `frontend/src/lib/status.ts` (`eventSummary`, lines 130-178)
- Test: `frontend/src/lib/status.test.ts` (create)

**Context (verified):** live payloads are `{"text","tool"}` (backend `runtime.go:35`) or `{"text"}` (`runtime.go:167`). The current tool branches read `p.tool_name` and model reads `p.call_count` — neither key exists, so real events fall back to the raw `event_type`. This task rewrites the tool/model branches to `p.tool` and makes the default branch labelled.

- [ ] **Step 1: Write the failing test file**

Create `frontend/src/lib/status.test.ts`:

```ts
import { describe, it, expect } from 'vitest'
import { eventSummary } from '@/lib/status'
import type { AgentEvent, EventType } from '@/lib/types'

function ev(type: EventType, payload: Record<string, unknown> = {}): AgentEvent {
  return {
    seq: 1,
    run_id: 'r1',
    event_type: type,
    payload_json: JSON.stringify(payload),
    sensitivity: 'normal',
    created_at: 0,
  }
}

describe('eventSummary', () => {
  it.each([
    ['tool.requested', 'requesting shell'],
    ['tool.started', 'tool shell started'],
    ['tool.completed', 'tool shell completed'],
    ['tool.failed', 'tool shell failed'],
    ['tool.authorized', 'tool shell authorized'],
    ['tool.denied', 'tool shell denied'],
  ] as const)('%s reads p.tool', (type, expected) => {
    expect(eventSummary(ev(type, { tool: 'shell' }))).toBe(expected)
  })

  it('falls back to static text when p.tool is absent', () => {
    expect(eventSummary(ev('tool.started'))).toBe('tool started')
  })

  it.each([
    ['model.requested', 'model call started'],
    ['model.completed', 'model call completed'],
    ['model.failed', 'model call failed'],
  ] as const)('%s has no call number (call_count is not in the payload)', (type, expected) => {
    expect(eventSummary(ev(type, { text: 'ok' }))).toBe(expected)
  })

  it('labels unknown event types instead of leaking the raw name', () => {
    expect(eventSummary(ev('weird.new.event' as EventType))).toBe('Unknown event weird.new.event')
  })
})
```

- [ ] **Step 2: Run, confirm failure**

Run (workdir `frontend/`): `npx vitest run src/lib/status.test.ts`
Expected: FAIL (current branches read the wrong keys / default returns the raw type).

- [ ] **Step 3: Rewrite the tool/model/default branches**

In `status.ts` `eventSummary`, replace the current tool cases (lines 133-139), model cases (lines 140-145), and the default (lines 175-176) with:

```ts
case 'tool.requested':
  return p.tool ? `requesting ${String(p.tool)}` : 'tool requested'
case 'tool.started':
  return p.tool ? `tool ${String(p.tool)} started` : 'tool started'
case 'tool.completed':
  return p.tool ? `tool ${String(p.tool)} completed` : 'tool completed'
case 'tool.failed':
  return p.tool ? `tool ${String(p.tool)} failed` : 'tool failed'
case 'tool.authorized':
  return p.tool ? `tool ${String(p.tool)} authorized` : 'tool authorized'
case 'tool.denied':
  return p.tool ? `tool ${String(p.tool)} denied` : 'tool denied'
case 'model.requested':
  return 'model call started'
case 'model.completed':
  return 'model call completed'
case 'model.failed':
  return 'model call failed'
```

and

```ts
default:
  return `Unknown event ${e.event_type}`
```

Do not touch the step/approval/run/plan/checkpoint/verification branches (spec §3: they already fall back to static text; only tool/model are rewritten this round).

- [ ] **Step 4: Run, confirm pass**

Run (workdir `frontend/`): `npx vitest run src/lib/status.test.ts`
Expected: 11 tests PASS (6 tool `it.each` tuples + 1 static-fallback + 3 model `it.each` tuples + 1 unknown-event).

- [ ] **Step 5: Commit**

```bash
git add frontend/src/lib/status.ts frontend/src/lib/status.test.ts
git commit -m "feat(event-summary): read p.tool payload key, label unknown events"
```

### Task 4: Empty-session placeholder in `ChatMessageList`

**Files:**
- Modify: `frontend/src/components/ChatMessageList.tsx`
- Test: `frontend/src/components/ChatMessageList.test.tsx`

**Context:** `ChatMessageList` currently renders an empty `<div>` when `messages` is empty. The placeholder must not disturb the layout (parent is `flex-1 overflow-y-auto p-4`).

- [ ] **Step 1: Add the failing test**

In `ChatMessageList.test.tsx`, add a test using the existing `seedStores` helper (its signature: `seedStores(activeRun, activeSteps, events, pendingGoals)`):

```tsx
it('renders a placeholder when there are no messages', () => {
  seedStores(null, [], [], [])
  render(<ChatMessageList />)
  expect(screen.getByText(/no tasks yet/i)).toBeTruthy()
})

it('does not render the placeholder when messages exist', () => {
  seedStores(null, [], [], [])
  useRunStore.setState({
    runHistory: [mkRun('succeeded', { result_json: '{"ok":true}' })],
    activeRun: null,
  })
  render(<ChatMessageList />)
  expect(screen.queryByText(/no tasks yet/i)).toBeNull()
})
```

- [ ] **Step 2: Run, confirm failure**

Run (workdir `frontend/`): `npx vitest run src/components/ChatMessageList.test.tsx`
Expected: the placeholder test ("renders a placeholder when there are no messages") FAILS — no such text is rendered today. The non-empty test ("does not render the placeholder when messages exist") passes vacuously (the placeholder isn't rendered anywhere yet, so `queryByText` is null as asserted).

- [ ] **Step 3: Implement the placeholder**

In `ChatMessageList.tsx`, after `handleRetry` and before the main `return`, insert the early-return. Also add `import { Inbox } from 'lucide-react'` to the file's imports (the project already uses lucide-react icons, e.g. `SessionList.tsx`):

```tsx
if (messages.length === 0) {
  return (
    <div className="flex flex-col items-center gap-2 rounded-lg border border-border bg-card/50 p-8 text-center">
      <Inbox className="h-5 w-5 text-muted-foreground/60" aria-hidden />
      <p className="text-sm font-medium">No tasks yet</p>
      <p className="text-xs text-muted-foreground">Describe a task below to start this session.</p>
    </div>
  )
}
```

- [ ] **Step 4: Run, confirm pass**

Run (workdir `frontend/`): `npx vitest run src/components/ChatMessageList.test.tsx`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/ChatMessageList.tsx frontend/src/components/ChatMessageList.test.tsx
git commit -m "feat(chat-message-list): empty-session placeholder"
```

### Task 5: `AssistantBubble` — cancelled neutral line

**Files:**
- Modify: `frontend/src/components/Messages/AssistantBubble.tsx`
- Test: `frontend/src/components/Messages/AssistantBubble.test.tsx`

**Context (verified):** backend cancel sets `error_code="cancelled"` (`run_service.go:89`), so today `AssistantBubble` swallows it into the rose "Run failed" box. Fix: a cancelled run renders a neutral line (no result) or result + "Cancelled — partial result" (with result). `timed_out` is deliberately not handled (unreachable; follow-ups §2).

- [ ] **Step 1: Add the failing tests**

In `AssistantBubble.test.tsx`, add (the `run()` helper is already in the file):

```tsx
it('renders a neutral line when error_code is "cancelled" and there is no result', () => {
  const run_ = { ...run('cancelled'), error_code: 'cancelled', error_summary: 'cancelled by user' } as Run
  render(<AssistantBubble run={run_} />)
  expect(screen.getByText(/run cancelled/i)).toBeTruthy()
})

it('renders the result plus a partial-result note when cancelled with a result', () => {
  const run_ = { ...run('cancelled', '{"ok":true}'), error_code: 'cancelled' } as Run
  render(<AssistantBubble run={run_} />)
  expect(screen.getByText(/cancelled — partial result/i)).toBeTruthy()
})
```

- [ ] **Step 2: Run, confirm failure**

Run (workdir `frontend/`): `npx vitest run src/components/Messages/AssistantBubble.test.tsx`
Expected: both new tests FAIL (cancelled hits the rose error box first).

- [ ] **Step 3: Implement the branch**

In `AssistantBubble.tsx`, insert **before** the `if (run.error_code)` check:

```tsx
if (run.error_code === 'cancelled') {
  if (run.result_json) {
    return (
      <div className="rounded-lg border border-border bg-card p-3">
        <p className="mb-2 text-xs font-medium text-muted-foreground">
          Cancelled — partial result
        </p>
        <ResultViewBlock resultJson={run.result_json} />
      </div>
    )
  }
  return (
    <p className="rounded-lg border border-border bg-card/50 p-3 text-sm text-muted-foreground">
      Run cancelled
    </p>
  )
}
```

- [ ] **Step 4: Run, confirm pass**

Run (workdir `frontend/`): `npx vitest run src/components/Messages/AssistantBubble.test.tsx`
Expected: 5 tests PASS (2 pre-existing + 2 new + the "no result, no error" test unchanged).

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/Messages/AssistantBubble.tsx frontend/src/components/Messages/AssistantBubble.test.tsx
git commit -m "feat(assistant-bubble): neutral cancelled line instead of rose error box"
```

---

## Chunk 3: Approval collapse

### Task 6: `deriveApprovals` — `expired` derivation + signature change

**Files:**
- Modify: `frontend/src/lib/chat-messages.ts`
- Test: `frontend/src/lib/chat-messages.test.ts`

**Context (verified):** backend `approval.requested` payloads carry no `approval_id`/`tool_name` keys (`runtime.go:100` emits `{"text":"{tool} requires approval"}`), so `deriveApprovals` returns `[]` for real data today — that stays. This task only (a) extends the `system-approval` state union with `'expired'`, (b) threads `runTerminal` into the derivation, (c) makes undecided-approval-on-terminal-run derive `expired`. The pending-card wiring ships with the backend read endpoint (follow-ups §1).

- [ ] **Step 1: Add the failing tests**

In `chat-messages.test.ts` (helpers `mkRun`, `mkEv` already exist), add:

```ts
it('derives expired when an approval was never decided and its run is terminal', () => {
  const run = mkRun('r1', 'failed')
  const out = deriveMessages({
    runHistory: [run],
    activeRun: run,
    activeSteps: [],
    eventsByRun: { r1: [mkEv(1, 'approval.requested', '{"approval_id":"a1","tool_name":"t1"}')] },
    pendingGoals: [],
  })
  const a = out.find((m) => m.kind === 'system-approval') as { state: string } | undefined
  expect(a?.state).toBe('expired')
})

it('keeps pending when the approval is undecided but the run is not terminal', () => {
  const run = mkRun('r1', 'waiting_approval')
  const out = deriveMessages({
    runHistory: [run],
    activeRun: run,
    activeSteps: [],
    eventsByRun: { r1: [mkEv(1, 'approval.requested', '{"approval_id":"a1","tool_name":"t1"}')] },
    pendingGoals: [],
  })
  const a = out.find((m) => m.kind === 'system-approval') as { state: string } | undefined
  expect(a?.state).toBe('pending')
})

it('derives no approval messages for real-data payloads (no approval_id key)', () => {
  const run = mkRun('r1', 'waiting_approval')
  const out = deriveMessages({
    runHistory: [run],
    activeRun: run,
    activeSteps: [],
    eventsByRun: { r1: [mkEv(1, 'approval.requested', '{"text":"shell requires approval"}')] },
    pendingGoals: [],
  })
  expect(out.filter((m) => m.kind === 'system-approval')).toHaveLength(0)
})
```

- [ ] **Step 2: Run, confirm failure**

Run (workdir `frontend/`): `npx vitest run src/lib/chat-messages.test.ts`
Expected: the first new test FAILS (state is `'pending'`, not `'expired'`); the other two PASS already (regression guards).

- [ ] **Step 3: Implement**

In `chat-messages.ts`:

1. Extend the `system-approval` union member (lines 40-46):

```ts
| {
    kind: 'system-approval'
    id: string
    runId: string
    event: AgentEvent
    state: 'pending' | 'approved' | 'rejected' | 'expired'
  }
```

2. Change the signature and state derivation in `deriveApprovals` (lines 153-187):

```ts
function deriveApprovals(runId: string, events: AgentEvent[], runTerminal: boolean): ChatMessage[] {
  // ... (the requested/decided Maps stay unchanged) ...
  for (const [approvalId, event] of requested) {
    const d = decided.get(approvalId)
    const state: 'pending' | 'approved' | 'rejected' | 'expired' = d
      ? (d.approved ? 'approved' : 'rejected')
      : runTerminal
        ? 'expired'
        : 'pending'
    out.push({ kind: 'system-approval', id: `approval-${runId}-${approvalId}`, runId, event, state })
  }
  // sort stays unchanged
}
```

3. Update the call site in `emitRun` (line 136):

```ts
const approvalMsgs = deriveApprovals(run.id, events, TERMINAL_RUN_STATUSES.includes(run.status))
```

- [ ] **Step 4: Run, confirm pass**

Run (workdir `frontend/`): `npx vitest run src/lib/chat-messages.test.ts`
Expected: all PASS (the pre-existing approval tests use `waiting_approval` runs, so they still see `'pending'`/`'approved'`/`'rejected'`).

- [ ] **Step 5: Commit**

```bash
git add frontend/src/lib/chat-messages.ts frontend/src/lib/chat-messages.test.ts
git commit -m "feat(chat-messages): derive expired approval state on terminal runs"
```

### Task 7: `ApprovalCard` — compact single-line resolved states

**Files:**
- Modify: `frontend/src/components/ApprovalCard.tsx`
- Test: `frontend/src/components/ApprovalCard.test.tsx`

**Context:** `ApprovalCard` already accepts `state: 'pending' | 'approved' | 'rejected' | 'expired'` (line 6) but today renders the full card for resolved states (badge + no buttons). This task makes resolved states a compact single line. `ApprovalBubble` needs no change: its `state={message.state === 'pending' ? 'pending' : message.state}` pass-through already handles the new `'expired'` value.

- [ ] **Step 1: Add the failing test**

In `ApprovalCard.test.tsx` (helper `ev` already exists):

```tsx
it('renders a compact single line with no buttons when expired', () => {
  render(
    <ApprovalCard event={ev('a1')} state="expired" onApprove={vi.fn()} onReject={vi.fn()} />,
  )
  expect(screen.getByText(/approval expired/i)).toBeTruthy()
  expect(screen.queryByRole('button', { name: /approve/i })).toBeNull()
})

it('renders a compact line with the tool name when approved', () => {
  render(
    <ApprovalCard event={ev('a1', 'shell')} state="approved" onApprove={vi.fn()} onReject={vi.fn()} />,
  )
  expect(screen.getByText(/✓ approved shell/i)).toBeTruthy()
})
```

- [ ] **Step 2: Run, confirm failure**

Run (workdir `frontend/`): `npx vitest run src/components/ApprovalCard.test.tsx`
Expected: BOTH new tests FAIL pre-change (today `state="expired"` renders the "Expired" badge, not the compact line; `state="approved"` renders the badge + tool `<code>`, not the `✓ Approved shell` line). Pre-existing tests (pending buttons, missing-id null) still pass.

- [ ] **Step 3: Implement the compact render**

In `ApprovalCard.tsx`, restructure the render. Move the `approvalId` early-return up (after the hooks) and insert the resolved branch:

```tsx
export function ApprovalCard({ event, state, onApprove, onReject }: Props) {
  const { approvalId, toolName } = parseApproval(event)
  const [rejectOpen, setRejectOpen] = useState(false)
  const [reason, setReason] = useState('')

  if (!approvalId) return null

  const badge = BADGE[state]
  const resolved = state === 'approved' || state === 'rejected' || state === 'expired'
  if (resolved) {
    const line =
      state === 'approved'
        ? `✓ Approved ${toolName}`
        : state === 'rejected'
          ? `✗ Rejected ${toolName}`
          : `Approval expired · ${toolName}`
    return <p className="text-xs text-muted-foreground">{line}</p>
  }

  return (
    // ... existing pending-card JSX, lines 42-102, UNCHANGED. It still references
    // `badge.cls`/`badge.label` (header) and the `{!resolved && (...)}` wrapper
    // around the buttons — so KEEP both consts. After the early return above, only
    // `pending` reaches this JSX, so `badge` === `BADGE.pending` and `!resolved`
    // is always true.
  )
}
```

Keep the `badge` and `resolved` consts (the pending-card JSX below still references both — `badge.cls`/`badge.label` in the header and the `{!resolved && (...)}` wrapper around the buttons). After the early return, only `pending` reaches that JSX, so `badge` is `BADGE.pending` and `!resolved` is always true. Do NOT delete either const. The `BADGE` map stays as-is (its non-`pending` entries become unreachable in the card JSX but are harmless; leave them).

- [ ] **Step 4: Run, confirm pass**

Run (workdir `frontend/`): `npx vitest run src/components/ApprovalCard.test.tsx`
Expected: all PASS. Then run the neighboring ChatMessageList approval test to confirm the pending-approval approve-button path is untouched: `npx vitest run src/components/ChatMessageList.test.tsx` (its "renders approval bubble wired to api.decideApproval" test must still pass).

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/ApprovalCard.tsx frontend/src/components/ApprovalCard.test.tsx
git commit -m "feat(approval-card): compact single-line resolved states (approved/rejected/expired)"
```

### Task 8: Full gate

**Files:** none (verification only).

- [ ] **Step 1: Typecheck**

Run (workdir `frontend/`): `npx tsc --noEmit`
Expected: exit 0.

- [ ] **Step 2: Full test suite**

Run (workdir `frontend/`): `npx vitest run`
Expected: all suites PASS.

- [ ] **Step 3: Lint**

Run (workdir `frontend/`): `npm run lint`
Expected: no new errors (pre-existing warnings acceptable — note them but do not fix unrelated files).

- [ ] **Step 4: Smoke (spec acceptance)**

Start backend + frontend (`make run` in `backend/` with model env vars; `npm run dev` in `frontend/`). In a fresh session: submit a task; while running, the input bar shows **Stop**; click **Stop**; the bubble turns into "Run cancelled" (neutral line) and the button returns to **Send**. Also: an empty session shows the placeholder. If no model endpoint is available in this environment, run the deterministic checks (steps 1-3) and report the smoke as visually-limited.

---

## Acceptance (this plan's completion criteria)

- Stop: `cancelling`/`cancelError` semantics per spec §1; button state machine Send ⇄ Stop driven by `isActiveRunStatus(activeRun.status)`; cancel refresh rides the existing SSE/poller REFRESH pipeline (no new channel).
- Approvals: `expired` derivable from `runTerminal`; compact lines for resolved states; real-data payloads still derive `[]` (pending-card wiring deferred to follow-ups §1).
- Events: tool branches read `p.tool`; model branches static; default `Unknown event {type}`.
- Empty session placeholder; cancelled neutral line / partial-result note.
- `npx tsc --noEmit` + `npx vitest run` green in `frontend/`.
- No backend changes.
