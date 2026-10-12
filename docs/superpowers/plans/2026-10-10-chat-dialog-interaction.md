# Chat Dialog Interaction Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the stacked RunForm/RunDetail/EventStream layout with a single chat panel: input bar + message stream, advanced config in a dialog, all run activity (status/steps/events/approvals/results) inline in the message stream.

**Architecture:** All messages are derived client-side from existing store state (`runHistory`, `activeRun`, `activeSteps`, `events`) plus a new `pendingGoals` queue; no new store. A pure function `deriveMessages` in `lib/chat-messages.ts` produces `ChatMessage[]`, which `ChatMessageList` renders by `kind`. New `run-store.submitRun` action handles optimistic user bubbles. `App.tsx` rewired to `Header + SessionList + ChatPanel`.

**Tech Stack:** React 18 + TypeScript + zustand + Radix Dialog (existing ui/dialog) + vite + vitest. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-10-10-chat-dialog-interaction-design.md`

**Key file map (before → after):**

| Existing | Disposition |
|---|---|
| `components/RunForm.tsx` | Delete (logic → `NewRunDialog.tsx` + `ChatInputBar.tsx`) |
| `components/RunDetail.tsx` | Delete (logic → `Messages/SystemStatusBubble.tsx` + `Messages/AssistantBubble.tsx`; `ResultViewBlock` exported from new `lib/result-block.tsx` so AssistantBubble uses it) |
| `components/EventStream.tsx` | Delete (event row rendering → `Messages/SystemEventsBubble.tsx`; filter chips/scroll removed per spec) |
| `components/SessionPanel.tsx` | Replace with `SessionList.tsx` (remove Run History section) |
| `components/ActionBar.tsx` | Delete (cancel/retry/resume buttons → `Messages/SystemStatusBubble.tsx`; the `#event-stream` scroll target no longer exists) |
| `components/ApprovalCard.tsx` | Refactor to controlled: props `{ event, state, onApprove, onReject }`; keep parse + countdown internally |
| `App.tsx` | Rewrite to `Header + SessionList + ChatPanel` |
| `store/run-store.ts` | Add `pendingGoals` queue + `submitRun` action |
| `ui-store.ts` | Remove `eventStreamOpen`/`toggleEventStream` (no longer used) |

**Test files to delete:** `RunForm.test.tsx`, `RunDetail.test.tsx`, `EventStream.test.tsx`, `ActionBar.test.tsx` (assertions migrated where noted below).

---

## Chunk 1: Foundation — types, deriveMessages, run-store.submitRun

### Task 1: Add `PendingGoal` type and re-export `ResultViewBlock` from a lib location

**Files:**
- Modify: `frontend/src/lib/types.ts`
- Create: `frontend/src/lib/result-block.tsx`
- Modify: `frontend/src/components/RunDetail.tsx` (keep for now; will delete in Chunk 3 — this task just moves the export so new components can import from `lib/result-block`)

- [ ] **Step 1: Add `PendingGoal` to types.ts**

Append to `frontend/src/lib/types.ts`:

```ts
export interface PendingGoal {
  id: string
  goal: string
  status: 'pending' | 'sent' | 'failed'
  error?: string
}
```

- [ ] **Step 2: Create `lib/result-block.tsx` containing `ResultViewBlock`**

The component body is currently in `RunDetail.tsx` (lines 113–147, already extracted). Create `frontend/src/lib/result-block.tsx` with exactly this content:

```tsx
import { useMemo } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import rehypeSanitize from 'rehype-sanitize'
import { detectResultView } from '@/lib/result-render'

export function ResultViewBlock({ resultJson }: { resultJson: string }) {
  const view = useMemo(() => {
    try {
      return detectResultView(resultJson)
    } catch {
      return { kind: 'raw', text: resultJson } as const
    }
  }, [resultJson])
  return (
    <details className="rounded-lg border border-border bg-card" open={view.kind === 'markdown'}>
      <summary className="cursor-pointer select-none px-3 py-2 text-xs font-medium text-muted-foreground hover:text-foreground">
        Result{view.kind === 'markdown' ? ' (markdown)' : ''}
      </summary>
      <div className="max-h-[400px] overflow-y-auto border-t border-border p-3">
        {view.kind === 'markdown' ? (
          <div className="space-y-2 text-sm text-foreground/90 [&_a]:text-primary [&_a]:underline [&_ol]:list-decimal [&_ol]:pl-5 [&_pre]:overflow-x-auto [&_pre]:rounded [&_pre]:bg-muted [&_pre]:p-2 [&_pre]:font-mono [&_pre]:text-xs [&_ul]:list-disc [&_ul]:pl-5">
            <ReactMarkdown remarkPlugins={[remarkGfm]} rehypePlugins={[rehypeSanitize]}>
              {view.text}
            </ReactMarkdown>
          </div>
        ) : (
          <pre className="overflow-x-auto font-mono text-xs text-foreground/90">
            {view.kind === 'json' ? view.pretty : view.text}
          </pre>
        )}
        <details className="mt-2">
          <summary className="cursor-pointer select-none text-[11px] text-muted-foreground/70 hover:text-foreground">
            Source
          </summary>
          <pre className="mt-1 overflow-x-auto font-mono text-[11px] text-muted-foreground/70">{resultJson}</pre>
        </details>
      </div>
    </details>
  )
}
```

- [ ] **Step 3: Make RunDetail re-export from the new location (temporarily, so existing RunDetail.test.tsx still passes)**

In `frontend/src/components/RunDetail.tsx`, delete the inline `ResultViewBlock` definition (lines 113–147) and add at the top:

```ts
export { ResultViewBlock } from '@/lib/result-block'
```

- [ ] **Step 4: Run existing tests to confirm nothing broke**

Run: `cd frontend && npx vitest run src/components/RunDetail.test.tsx`
Expected: PASS (RunDetail still exports ResultViewBlock via re-export)

- [ ] **Step 5: Commit**

```bash
git add frontend/src/lib/types.ts frontend/src/lib/result-block.tsx frontend/src/components/RunDetail.tsx
git commit -m "refactor: extract ResultViewBlock to lib/result-block, add PendingGoal type"
```

---

### Task 2: `lib/chat-messages.ts` — `deriveMessages` pure function

**Files:**
- Create: `frontend/src/lib/chat-messages.ts`
- Create: `frontend/src/lib/chat-messages.test.ts`

- [ ] **Step 1: Write the failing tests**

Create `frontend/src/lib/chat-messages.test.ts`:

```ts
import { describe, it, expect } from 'vitest'
import { deriveMessages, type ChatMessage } from '@/lib/chat-messages'
import type { Run, AgentEvent } from '@/lib/types'

function mkRun(id: string, status: Run['status'], extra: Partial<Run> = {}): Run {
  return {
    id,
    session_id: 's1',
    owner_id: 'o1',
    status,
    mode: 'react',
    goal: `goal-${id}`,
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
    ...extra,
  }
}

function mkEv(seq: number, type: AgentEvent['event_type'], payload = '{}'): AgentEvent {
  return {
    seq,
    run_id: 'r1',
    event_type: type,
    payload_json: payload,
    sensitivity: 'normal',
    created_at: 0,
  }
}

describe('deriveMessages', () => {
  it('returns empty array when no history, no pending goals', () => {
    const out = deriveMessages({
      runHistory: [],
      activeRun: null,
      activeSteps: [],
      eventsByRun: {},
      pendingGoals: [],
    })
    expect(out).toEqual([])
  })

  it('emits user + system-status bubbles for a terminal run', () => {
    const run = mkRun('r1', 'succeeded', { result_json: '{"ok":true}' })
    const out = deriveMessages({
      runHistory: [run],
      activeRun: run,
      activeSteps: [],
      eventsByRun: {},
      pendingGoals: [],
    })
    const kinds = out.map((m) => m.kind)
    expect(kinds).toContain('user')
    expect(kinds).toContain('system-status')
    expect(kinds).toContain('assistant')
  })

  it('does not emit assistant bubble for non-terminal run', () => {
    const run = mkRun('r1', 'running')
    const out = deriveMessages({
      runHistory: [run],
      activeRun: run,
      activeSteps: [{ id: 'st1', run_id: 'r1', step_index: 1, description: 'd', status: 'running' }],
      eventsByRun: {},
      pendingGoals: [],
    })
    expect(out.map((m) => m.kind)).not.toContain('assistant')
  })

  it('emits pending user bubble with pending status from pendingGoals', () => {
    const out = deriveMessages({
      runHistory: [],
      activeRun: null,
      activeSteps: [],
      eventsByRun: {},
      pendingGoals: [{ id: 'p1', goal: 'g', status: 'pending' }],
    })
    expect(out).toHaveLength(1)
    expect(out[0]).toMatchObject({ kind: 'user', goal: 'g', status: 'pending' })
  })

  it('emits failed user bubble preserving goal text and error', () => {
    const out = deriveMessages({
      runHistory: [],
      activeRun: null,
      activeSteps: [],
      eventsByRun: {},
      pendingGoals: [{ id: 'p1', goal: 'g', status: 'failed', error: 'boom' }],
    })
    expect(out[0]).toMatchObject({ kind: 'user', goal: 'g', status: 'failed', error: 'boom' })
  })

  it('orders multiple runs by created_at ascending', () => {
    const r1 = mkRun('r1', 'succeeded', { created_at: 1 })
    const r2 = mkRun('r2', 'succeeded', { created_at: 2 })
    const out = deriveMessages({
      runHistory: [r2, r1], // intentionally reversed input
      activeRun: null,
      activeSteps: [],
      eventsByRun: {},
      pendingGoals: [],
    })
    const userGoals = out.filter((m): m is Extract<ChatMessage, { kind: 'user' }> => m.kind === 'user')
    expect(userGoals.map((m) => m.runId)).toEqual(['r1', 'r2'])
  })

  it('emits system-approval bubble for approval.requested events, ordered by seq, merging multiple approvals per run', () => {
    const run = mkRun('r1', 'waiting_approval')
    const out = deriveMessages({
      runHistory: [run],
      activeRun: run,
      activeSteps: [],
      eventsByRun: {
        r1: [
          mkEv(2, 'approval.requested', '{"approval_id":"a1","tool_name":"t1"}'),
          mkEv(1, 'approval.requested', '{"approval_id":"a0","tool_name":"t0"}'), // out of order input
        ],
      },
      pendingGoals: [],
    })
    const approvals = out.filter((m) => m.kind === 'system-approval')
    expect(approvals).toHaveLength(2)
    const ids = approvals.map((m) => (m as { runId?: string }).runId)
    expect(ids).toEqual(['r1', 'r1'])
    // a0 (seq 1) must come before a1 (seq 2) in the output
    const first = approvals[0] as { state: string; event: AgentEvent }
    expect(JSON.parse(first.event.payload_json).approval_id).toBe('a0')
    expect(first.state).toBe('pending')
  })

  it('marks approval as approved when a later approval.decided event exists', () => {
    const run = mkRun('r1', 'waiting_approval')
    const out = deriveMessages({
      runHistory: [run],
      activeRun: run,
      activeSteps: [],
      eventsByRun: {
        r1: [
          mkEv(1, 'approval.requested', '{"approval_id":"a1","tool_name":"t1"}'),
          mkEv(2, 'approval.decided', '{"approval_id":"a1","approved":true}'),
        ],
      },
      pendingGoals: [],
    })
    const approval = out.find((m) => m.kind === 'system-approval') as
      | { state: string; approval: { id: string } }
      | undefined
    expect(approval?.state).toBe('approved')
    // only one bubble, not two
    expect(out.filter((m) => m.kind === 'system-approval')).toHaveLength(1)
  })

  it('marks approval as rejected when approval.decided has approved=false', () => {
    const run = mkRun('r1', 'waiting_approval')
    const out = deriveMessages({
      runHistory: [run],
      activeRun: run,
      activeSteps: [],
      eventsByRun: {
        r1: [
          mkEv(1, 'approval.requested', '{"approval_id":"a1","tool_name":"t1"}'),
          mkEv(2, 'approval.decided', '{"approval_id":"a1","approved":false,"reason":"nope"}'),
        ],
      },
      pendingGoals: [],
    })
    const approval = out.find((m) => m.kind === 'system-approval') as
      | { state: string }
      | undefined
    expect(approval?.state).toBe('rejected')
  })

  it('injects activeSteps and activeRun budget into the active run system-status bubble', () => {
    const run = mkRun('r1', 'running')
    const steps = [{ id: 'st1', run_id: 'r1', step_index: 1, description: 'd', status: 'running' }]
    const out = deriveMessages({
      runHistory: [run],
      activeRun: run,
      activeSteps: steps,
      eventsByRun: {},
      pendingGoals: [],
    })
    const status = out.find((m) => m.kind === 'system-status') as
      | { steps: { id: string }[]; budget: { max_model_calls: number } }
      | undefined
    expect(status?.steps).toEqual(steps)
    expect(status?.budget.max_model_calls).toBe(10)
  })

  it('does not double-emit activeRun when it also appears in runHistory', () => {
    const run = mkRun('r1', 'succeeded', { result_json: '{"ok":true}' })
    const out = deriveMessages({
      runHistory: [run],
      activeRun: run,
      activeSteps: [],
      eventsByRun: {},
      pendingGoals: [],
    })
    const statusCount = out.filter((m) => m.kind === 'system-status').length
    expect(statusCount).toBe(1)
  })
})
```

- [ ] **Step 2: Run test to confirm it fails**

Run: `cd frontend && npx vitest run src/lib/chat-messages.test.ts`
Expected: FAIL with "Cannot find module '@/lib/chat-messages'"

- [ ] **Step 3: Implement `lib/chat-messages.ts`**

Create `frontend/src/lib/chat-messages.ts`:

```ts
import type { AgentEvent, Budget, PendingGoal, Run, Step } from './types'
import { TERMINAL_RUN_STATUSES } from './status'

export type ChatMessage =
  | {
      kind: 'user'
      id: string
      runId: string
      goal: string
      status: 'pending' | 'sent' | 'failed'
      error?: string
    }
  | {
      kind: 'assistant'
      id: string
      runId: string
      run: Run
      resultJson?: string
      error?: { code: string; summary?: string }
    }
  | {
      kind: 'system-status'
      id: string
      runId: string
      run: Run
      status: Run['status']
      mode: string
      goal: string
      steps: Step[]
      budget: Budget
      error?: { code: string; summary?: string }
    }
  | {
      kind: 'system-events'
      id: string
      runId: string
      events: AgentEvent[]
      collapsed: boolean
    }
  | {
      kind: 'system-approval'
      id: string
      runId: string
      event: AgentEvent
      state: 'pending' | 'approved' | 'rejected'
    }

export interface DeriveInput {
  runHistory: Run[]
  activeRun: Run | null
  activeSteps: Step[]
  eventsByRun: Record<string, AgentEvent[]>
  pendingGoals: PendingGoal[]
}

/**
 * Derive the chat message list from all existing store state. Pure function,
 * no side effects, no API calls. Order: pending goals first (they are the
 * newest, not-yet-committed turns), then runs by created_at ascending.
 */
export function deriveMessages(input: DeriveInput): ChatMessage[] {
  const { runHistory, activeRun, activeSteps, eventsByRun, pendingGoals } = input

  const messages: ChatMessage[] = []

  for (const p of pendingGoals) {
    messages.push({
      kind: 'user',
      id: p.id,
      runId: p.id,
      goal: p.goal,
      status: p.status,
      error: p.error,
    })
  }

  // De-dupe activeRun against runHistory (it will usually already be in runHistory
  // after fetchRunAndSteps refresh, but keep it explicit so we never emit two
  // status bubbles for the same run id).
  const seen = new Set<string>()
  const ordered = [...runHistory].sort((a, b) => a.created_at - b.created_at)
  for (const run of ordered) {
    if (run.id === activeRun?.id) continue
    seen.add(run.id)
    emitRun(messages, run, activeSteps, eventsByRun)
  }
  if (activeRun && !seen.has(activeRun.id)) {
    emitRun(messages, activeRun, activeSteps, eventsByRun)
  }

  return messages
}

function emitRun(
  messages: ChatMessage[],
  run: Run,
  steps: Step[],
  eventsByRun: Record<string, AgentEvent[]>,
) {
  const userMsg: ChatMessage = {
    kind: 'user',
    id: `user-${run.id}`,
    runId: run.id,
    goal: run.goal,
    status: 'sent',
  }
  messages.push(userMsg)

  const statusMsg: ChatMessage = {
    kind: 'system-status',
    id: `status-${run.id}`,
    runId: run.id,
    run,
    status: run.status,
    mode: run.mode,
    goal: run.goal,
    steps,
    budget: run.budget,
    error: run.error_code
      ? { code: run.error_code, summary: run.error_summary }
      : undefined,
  }
  messages.push(statusMsg)

  const events = eventsByRun[run.id] ?? []
  if (events.length > 0) {
    messages.push({
      kind: 'system-events',
      id: `events-${run.id}`,
      runId: run.id,
      events,
      collapsed: true,
    })
  }

  const approvalMsgs = deriveApprovals(run.id, events)
  messages.push(...approvalMsgs)

  if (TERMINAL_RUN_STATUSES.includes(run.status)) {
    messages.push({
      kind: 'assistant',
      id: `assistant-${run.id}`,
      runId: run.id,
      run,
      resultJson: run.result_json,
      error: run.error_code
        ? { code: run.error_code, summary: run.error_summary }
        : undefined,
    })
  }
}

function deriveApprovals(runId: string, events: AgentEvent[]): ChatMessage[] {
  const sorted = [...events].sort((a, b) => a.seq - b.seq)
  const requested = new Map<string, AgentEvent>()
  const decided = new Map<string, { approved: boolean; reason?: string }>()
  for (const e of sorted) {
    if (e.event_type === 'approval.requested') {
      const p = parseApprovalPayload(e)
      if (p.approvalId) requested.set(p.approvalId, e)
    } else if (e.event_type === 'approval.decided') {
      const p = parseApprovalPayload(e)
      if (p.approvalId) decided.set(p.approvalId, { approved: p.approved, reason: p.reason })
    }
  }

  const out: ChatMessage[] = []
  for (const [approvalId, event] of requested) {
    const d = decided.get(approvalId)
    const state: 'pending' | 'approved' | 'rejected' = d
      ? (d.approved ? 'approved' : 'rejected')
      : 'pending'
    out.push({
      kind: 'system-approval',
      id: `approval-${runId}-${approvalId}`,
      runId,
      event,
      state,
    })
  }
  out.sort((a, b) => (a.event.seq - b.event.seq))
  return out
}

function parseApprovalPayload(e: AgentEvent) {
  let approvalId = ''
  let approved: boolean | undefined
  let reason: string | undefined
  try {
    const p = JSON.parse(e.payload_json || '{}')
    approvalId = p.approval_id ?? p.approvalId ?? ''
    approved = p.approved
    reason = p.reason
  } catch {
    // leave defaults
  }
  return { approvalId, approved, reason }
}
```

Note: import `PendingGoal` from `./types` (added in Task 1). The spec's ChatMessage type used `approval: Approval` but there is no separate `Approval` fetch endpoint wired into the frontend — the approval data comes from the `approval.requested` event payload, so `system-approval` carries the raw `AgentEvent` and its derived `state`. `ApprovalBubble` (Chunk 2) renders `ApprovalCard` from that event.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd frontend && npx vitest run src/lib/chat-messages.test.ts`
Expected: PASS (all 10 tests)

- [ ] **Step 5: Commit**

```bash
git add frontend/src/lib/chat-messages.ts frontend/src/lib/chat-messages.test.ts
git commit -m "feat: add deriveMessages pure function for chat message derivation"
```

---

### Task 3: `run-store.submitRun` action + `eventsByRun` selector

**Files:**
- Modify: `frontend/src/store/run-store.ts`
- Modify: `frontend/src/store/run-store.test.ts`

- [ ] **Step 1: Write the failing tests**

Append to `frontend/src/store/run-store.test.ts`:

```ts
  it('submitRun: optimistic pending goal, clears on success, activates run', async () => {
    const createRun = vi.fn().mockResolvedValue({ run_id: 'r9', status: 'queued', duplicated: false })
    const activateRun = vi.fn().mockResolvedValue(undefined)
    vi.mocked(api.createRun) = createRun
    useRunStore.setState({ activateRun })

    useRunStore.getState().submitRun({
      sessionId: 's1',
      goal: 'do thing',
      mode: 'react',
      constraints: undefined,
      successCriteria: undefined,
      budget: undefined,
    })

    const pending = useRunStore.getState().pendingGoals
    expect(pending).toHaveLength(1)
    expect(pending[0]).toMatchObject({ goal: 'do thing', status: 'pending' })

    await vi.waitFor(() => {
      expect(useRunStore.getState().pendingGoals).toHaveLength(0)
    })
    expect(createRun).toHaveBeenCalledWith(
      expect.objectContaining({ session_id: 's1', goal: 'do thing' }),
      expect.any(String),
    )
    expect(activateRun).toHaveBeenCalledWith('r9')
  })

  it('submitRun: on failure, keeps goal with failed status + error, does not activate', async () => {
    const createRun = vi.fn().mockRejectedValue(new Error('network down'))
    const activateRun = vi.fn().mockResolvedValue(undefined)
    vi.mocked(api.createRun) = createRun
    useRunStore.setState({ activateRun })

    const promise = useRunStore.getState().submitRun({
      sessionId: 's1',
      goal: 'do thing',
      mode: 'react',
      constraints: undefined,
      successCriteria: undefined,
      budget: undefined,
    })

    await vi.waitFor(() => {
      const p = useRunStore.getState().pendingGoals[0]
      expect(p?.status).toBe('failed')
    })
    await promise.catch(() => {})
    expect(useRunStore.getState().pendingGoals[0]).toMatchObject({
      goal: 'do thing',
      status: 'failed',
      error: 'network down',
    })
    expect(activateRun).not.toHaveBeenCalled()
  })

  it('submitRun: clears pendingGoals after a successful submit of a later goal', async () => {
    const activateRun = vi.fn().mockResolvedValue(undefined)
    useRunStore.setState({ activateRun })

    // Manually seed a failed pending goal (as if a prior submit failed)
    useRunStore.setState({
      pendingGoals: [{ id: 'p-old', goal: 'stale', status: 'failed', error: 'x' }],
    })
    expect(useRunStore.getState().pendingGoals).toHaveLength(1)

    vi.mocked(api.createRun).mockResolvedValue({ run_id: 'r10', status: 'queued', duplicated: false })
    await useRunStore.getState().submitRun({
      sessionId: 's1',
      goal: 'retry works',
      mode: 'react',
      constraints: undefined,
      successCriteria: undefined,
      budget: undefined,
    })
    // After successful submit, all pendingGoals are cleared (the stale failed one is dropped too)
    expect(useRunStore.getState().pendingGoals).toHaveLength(0)
  })
```

Also add to the top-level `beforeEach` reset:

```ts
useRunStore.setState({
  activeRunId: null,
  activeRun: null,
  activeSteps: [],
  events: [],
  eventFilter: 'all',
  runHistory: [],
  pendingGoals: [],
})
```

The existing mock at the top of the file only mocks `getRun`/`listRuns`; extend it:

```ts
vi.mock('@/lib/api', () => ({
  api: {
    getRun: vi.fn().mockResolvedValue({ run: {}, steps: [] }),
    listRuns: vi.fn().mockResolvedValue([]),
    createRun: vi.fn().mockResolvedValue({ run_id: 'r1', status: 'queued', duplicated: false }),
  },
}))
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd frontend && npx vitest run src/store/run-store.test.ts`
Expected: FAIL — `submitRun` is not a function / `pendingGoals` not in state

- [ ] **Step 3: Implement the new state + action in run-store.ts**

Rewrite `frontend/src/store/run-store.ts`:

```ts
import { create } from 'zustand'
import { api, ApiError } from '@/lib/api'
import { MAX_EVENTS } from '@/App.config'
import type { AgentEvent, Budget, EventType, PendingGoal, Run, Step } from '@/lib/types'

export interface SubmitRunInput {
  sessionId: string
  goal: string
  mode?: 'react' | 'plan_execute'
  constraints?: string[]
  successCriteria?: string[]
  budget?: Partial<Budget>
}

interface RunState {
  activeRunId: string | null
  activeRun: Run | null
  activeSteps: Step[]
  events: AgentEvent[]
  eventFilter: EventType | 'all'
  runHistory: Run[]
  pendingGoals: PendingGoal[]
  activateRun: (id: string) => Promise<void>
  fetchRunAndSteps: (id: string) => Promise<void>
  appendEvent: (e: AgentEvent) => void
  setEventFilter: (f: EventType | 'all') => void
  clearEvents: () => void
  loadRunHistory: (sessionId: string) => Promise<void>
  submitRun: (input: SubmitRunInput) => Promise<void>
  dismissPendingGoal: (id: string) => void
}

const TERMINAL_EVENTS: ReadonlySet<EventType> = new Set([
  'run.finished',
  'run.failed',
  'run.cancelled',
])

function pendingGoalId(): string {
  return `pending-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
}

export const useRunStore = create<RunState>((set, get) => ({
  activeRunId: null,
  activeRun: null,
  activeSteps: [],
  events: [],
  eventFilter: 'all',
  runHistory: [],
  pendingGoals: [],

  activateRun: async (id) => {
    set({ activeRunId: id, events: [] })
    await get().fetchRunAndSteps(id)
  },

  fetchRunAndSteps: async (id) => {
    const { run, steps } = await api.getRun(id)
    set({ activeRun: run, activeSteps: steps })
  },

  appendEvent: (e) => {
    const cur = get().events
    if (cur.some((x) => x.seq === e.seq && x.event_type === e.event_type)) return
    const events = [...cur, e]
    if (events.length > MAX_EVENTS) {
      events.splice(0, events.length - MAX_EVENTS)
    }
    set({ events })
    if (TERMINAL_EVENTS.has(e.event_type)) {
      const id = get().activeRunId
      if (id) void get().fetchRunAndSteps(id)
    }
  },

  setEventFilter: (f) => set({ eventFilter: f }),

  clearEvents: () => set({ events: [] }),

  loadRunHistory: async (sessionId) => {
    const runs = await api.listRuns(sessionId)
    set({ runHistory: runs, pendingGoals: [] })
  },

  submitRun: async (input) => {
    const goalId = pendingGoalId()
    set((s) => ({
      pendingGoals: [
        ...s.pendingGoals,
        { id: goalId, goal: input.goal, status: 'pending' },
      ],
    }))
    try {
      const idempotencyKey = crypto.randomUUID()
      const res = await api.createRun(
        {
          session_id: input.sessionId,
          goal: input.goal,
          mode: input.mode,
          constraints: input.constraints,
          success_criteria: input.successCriteria,
          budget: input.budget,
        },
        idempotencyKey,
      )
      // Success: clear the whole pending queue and activate the new run.
      set({ pendingGoals: [] })
      await get().activateRun(res.run_id)
    } catch (e) {
      const msg = e instanceof Error ? e.message : '创建 Run 失败'
      set((s) => ({
        pendingGoals: s.pendingGoals.map((p) =>
          p.id === goalId ? { ...p, status: 'failed', error: msg } : p,
        ),
      }))
    }
  },

  dismissPendingGoal: (id) =>
    set((s) => ({ pendingGoals: s.pendingGoals.filter((p) => p.id !== id) })),
}))
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd frontend && npx vitest run src/store/run-store.test.ts`
Expected: PASS (all tests including the new submitRun tests)

- [ ] **Step 5: Commit**

```bash
git add frontend/src/store/run-store.ts frontend/src/store/run-store.test.ts
git commit -m "feat: add submitRun action with optimistic pending goal queue to run-store"
```

---

### Task 4: `eventsByRun` selector helper

**Files:**
- Create: `frontend/src/store/selectors.ts`
- Create: `frontend/src/store/selectors.test.ts`

- [ ] **Step 1: Write the failing tests**

Create `frontend/src/store/selectors.test.ts`:

```ts
import { describe, it, expect } from 'vitest'
import { groupEventsByRun } from '@/store/selectors'
import type { AgentEvent } from '@/lib/types'

function ev(runId: string, seq: number, type: AgentEvent['event_type'] = 'run.started'): AgentEvent {
  return {
    seq,
    run_id: runId,
    event_type: type,
    payload_json: '{}',
    sensitivity: 'normal',
    created_at: 0,
  }
}

describe('groupEventsByRun', () => {
  it('groups a flat event list by run_id preserving seq order', () => {
    const events = [ev('r1', 1), ev('r2', 1), ev('r1', 2), ev('r2', 2)]
    const out = groupEventsByRun(events)
    expect(out.r1?.map((e) => e.seq)).toEqual([1, 2])
    expect(out.r2?.map((e) => e.seq)).toEqual([1, 2])
  })

  it('returns an empty object for an empty list', () => {
    expect(groupEventsByRun([])).toEqual({})
  })

  it('does not mutate the input array', () => {
    const events = [ev('r1', 1), ev('r1', 2)]
    groupEventsByRun(events)
    expect(events).toEqual([ev('r1', 1), ev('r1', 2)])
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd frontend && npx vitest run src/store/selectors.test.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Implement**

Create `frontend/src/store/selectors.ts`:

```ts
import type { AgentEvent } from '@/lib/types'

export function groupEventsByRun(events: AgentEvent[]): Record<string, AgentEvent[]> {
  const out: Record<string, AgentEvent[]> = {}
  for (const e of events) {
    const list = (out[e.run_id] ??= [])
    list.push(e)
  }
  return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd frontend && npx vitest run src/store/selectors.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add frontend/src/store/selectors.ts frontend/src/store/selectors.test.ts
git commit -m "feat: add groupEventsByRun selector for chat message derivation"
```

---

## Chunk 2: Components — ChatPanel, ChatInputBar, ChatMessageList, Message Bubbles, NewRunDialog, controlled ApprovalCard, SessionList, ui-store cleanup

### Task 5: Refactor `ApprovalCard` to a controlled component

**Files:**
- Modify: `frontend/src/components/ApprovalCard.tsx`
- Modify: `frontend/src/components/ApprovalCard.test.tsx`

The current `ApprovalCard` manages its own decided state and calls `api.decideApproval` + `fetchRunAndSteps` internally. The new version is purely presentational: it receives a `state` (`pending | approved | rejected | expired`) and fires `onApprove()` / `onReject(reason: string)` callbacks. The countdown timer stays internal (it only drives the `expired` display, which is harmless since the parent re-derives state from events anyway — the `expired` internal state is kept purely for local UI display of the countdown hitting zero while the server decision hasn't arrived yet).

- [ ] **Step 1: Rewrite ApprovalCard.test.tsx for the new controlled contract**

```tsx
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { ApprovalCard } from '@/components/ApprovalCard'
import type { AgentEvent } from '@/lib/types'

function ev(approvalId: string, toolName = 'shell'): AgentEvent {
  return {
    seq: 1,
    run_id: 'r1',
    event_type: 'approval.requested',
    payload_json: JSON.stringify({ approval_id: approvalId, tool_name: toolName }),
    sensitivity: 'normal',
    created_at: 0,
  }
}

describe('ApprovalCard (controlled)', () => {
  it('renders tool name and approve/reject buttons when pending', () => {
    const onApprove = vi.fn()
    const onReject = vi.fn()
    render(<ApprovalCard event={ev('a1', 'shell')} state="pending" onApprove={onApprove} onReject={onReject} />)
    expect(screen.getByText(/shell/i)).toBeTruthy()
    expect(screen.getByRole('button', { name: /approve/i })).toBeTruthy()
    expect(screen.getByRole('button', { name: /reject/i })).toBeTruthy()
  })

  it('fires onApprove with no argument', () => {
    const onApprove = vi.fn()
    render(<ApprovalCard event={ev('a1')} state="pending" onApprove={onApprove} onReject={vi.fn()} />)
    fireEvent.click(screen.getByRole('button', { name: /approve/i }))
    expect(onApprove).toHaveBeenCalledTimes(1)
    expect(onApprove).toHaveBeenCalledWith()
  })

  it('opens reject dialog and fires onReject with reason', async () => {
    const onReject = vi.fn()
    render(<ApprovalCard event={ev('a1')} state="pending" onApprove={vi.fn()} onReject={onReject} />)
    fireEvent.click(screen.getByRole('button', { name: /reject/i }))
    const textarea = await screen.findByPlaceholderText(/reason/i)
    fireEvent.change(textarea, { target: { value: 'too risky' } })
    fireEvent.click(screen.getByRole('button', { name: /confirm|submit/i }))
    expect(onReject).toHaveBeenCalledWith('too risky')
  })

  it('shows a resolved badge and no action buttons when state is approved', () => {
    render(<ApprovalCard event={ev('a1')} state="approved" onApprove={vi.fn()} onReject={vi.fn()} />)
    expect(screen.queryByRole('button', { name: /approve/i })).toBeNull()
    expect(screen.getByText(/approved/i)).toBeTruthy()
  })

  it('shows a resolved badge and no action buttons when state is rejected', () => {
    render(<ApprovalCard event={ev('a1')} state="rejected" onApprove={vi.fn()} onReject={vi.fn()} />)
    expect(screen.queryByRole('button', { name: /reject/i })).toBeNull()
    expect(screen.getByText(/rejected/i)).toBeTruthy()
  })

  it('renders nothing when approval_id is missing from payload', () => {
    const onApprove = vi.fn()
    const { container } = render(
      <ApprovalCard event={ev('', 'shell')} state="pending" onApprove={onApprove} onReject={vi.fn()} />,
    )
    expect(container.firstChild).toBeNull()
  })
})
```

Note: the `ev('', 'shell')` case must return `null` from the component — keep the existing `if (!approvalId) return null` guard but move it to read the prop-derived `state`; since `state` is now a prop and there's no self-computed decided state, the guard simply checks the parsed `approvalId` from the event payload.

- [ ] **Step 2: Run tests to confirm they fail**

Run: `cd frontend && npx vitest run src/components/ApprovalCard.test.tsx`
Expected: FAIL — old component has no `state`/`onApprove`/`onReject` props

- [ ] **Step 3: Rewrite ApprovalCard**

```tsx
import { useState } from 'react'
import { useRunStore } from '@/store/run-store'
import { cn } from '@/lib/utils'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogTrigger, DialogFooter } from '@/components/ui/dialog'

interface Props {
  event: import('@/lib/types').AgentEvent
  state: 'pending' | 'approved' | 'rejected' | 'expired'
  onApprove: () => void
  onReject: (reason: string) => void
}

function parseApproval(event: Props['event']) {
  let p: Record<string, unknown> = {}
  try {
    p = JSON.parse(event.payload_json || '{}')
  } catch {
    p = {}
  }
  return {
    approvalId: (p.approval_id as string) ?? (p.approvalId as string) ?? '',
    toolName: (p.tool_name as string) ?? (p.toolName as string) ?? 'tool',
  }
}

const BADGE: Record<Props['state'], { cls: string; label: string }> = {
  pending: { cls: 'bg-amber-500/15 text-amber-600 dark:text-amber-400', label: 'Pending' },
  approved: { cls: 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400', label: 'Approved' },
  rejected: { cls: 'bg-rose-500/15 text-rose-600 dark:text-rose-400', label: 'Rejected' },
  expired: { cls: 'bg-muted text-muted-foreground', label: 'Expired' },
}

export function ApprovalCard({ event, state, onApprove, onReject }: Props) {
  const { approvalId, toolName } = parseApproval(event)
  const [rejectOpen, setRejectOpen] = useState(false)
  const [reason, setReason] = useState('')

  if (!approvalId) return null

  const badge = BADGE[state]
  const resolved = state === 'approved' || state === 'rejected' || state === 'expired'

  return (
    <div className="rounded-lg border border-border bg-card p-3">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2 text-sm">
          <span className="font-medium">Tool approval</span>
          <span className="text-muted-foreground">·</span>
          <code className="text-xs">{toolName}</code>
        </div>
        <span className={cn('rounded-full px-2 py-0.5 text-[11px]', badge.cls)}>{badge.label}</span>
      </div>
      {!resolved && (
        <div className="mt-3 flex items-center gap-2">
          <button
            type="button"
            className="rounded-md bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:bg-primary/90"
            onClick={onApprove}
          >
            Approve
          </button>
          <Dialog open={rejectOpen} onOpenChange={setRejectOpen}>
            <DialogTrigger className="inline-flex">
              <button
                type="button"
                className="rounded-md border border-border px-3 py-1.5 text-xs font-medium hover:bg-accent"
              >
                Reject
              </button>
            </DialogTrigger>
            <DialogContent className="sm:max-w-[420px]">
              <DialogHeader>
                <DialogTitle>Reject approval</DialogTitle>
              </DialogHeader>
              <textarea
                className="w-full rounded-md border border-border bg-background p-2 text-sm"
                rows={3}
                placeholder="Reason (optional)"
                value={reason}
                onChange={(e) => setReason(e.target.value)}
              />
              <DialogFooter>
                <button
                  type="button"
                  className="rounded-md border border-border px-3 py-1.5 text-xs hover:bg-accent"
                  onClick={() => setRejectOpen(false)}
                >
                  Cancel
                </button>
                <button
                  type="button"
                  className="rounded-md bg-rose-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-rose-700"
                  onClick={() => {
                    setRejectOpen(false)
                    onReject(reason.trim())
                  }}
                >
                  Confirm
                </button>
              </DialogFooter>
            </DialogContent>
          </Dialog>
        </div>
      )}
    </div>
  )
}
```

Important: remove the unused `useRunStore` import if it ends up unused in the rewritten component (the parent now owns the API call). Keep imports minimal.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd frontend && npx vitest run src/components/ApprovalCard.test.tsx`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/ApprovalCard.tsx frontend/src/components/ApprovalCard.test.tsx
git commit -m "refactor: make ApprovalCard a controlled component with onApprove/onReject props"
```

---

### Task 6: `Messages/` bubble components

**Files:**
- Create: `frontend/src/components/Messages/UserBubble.tsx`
- Create: `frontend/src/components/Messages/AssistantBubble.tsx`
- Create: `frontend/src/components/Messages/SystemStatusBubble.tsx`
- Create: `frontend/src/components/Messages/SystemEventsBubble.tsx`
- Create: `frontend/src/components/Messages/ApprovalBubble.tsx`
- Create: `frontend/src/components/Messages/index.ts`
- Create: `frontend/src/components/Messages/SystemStatusBubble.test.tsx`
- Create: `frontend/src/components/Messages/AssistantBubble.test.tsx`

- [ ] **Step 1: Write failing tests**

`frontend/src/components/Messages/SystemStatusBubble.test.tsx`:

```tsx
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { SystemStatusBubble } from './SystemStatusBubble'
import type { Run } from '@/lib/types'

function run(status: Run['status']): Run {
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

describe('SystemStatusBubble', () => {
  it('renders a cancel button for an active (running) run and calls onCancelRun', () => {
    const onCancelRun = vi.fn()
    const onRetryRun = vi.fn()
    render(<SystemStatusBubble run={run('running')} onCancelRun={onCancelRun} onRetryRun={onRetryRun} />)
    expect(screen.getByRole('button', { name: /cancel/i })).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: /cancel/i }))
    expect(onCancelRun).toHaveBeenCalledWith('r1')
  })

  it('renders a retry button for a failed run and calls onRetryRun', () => {
    const onCancelRun = vi.fn()
    const onRetryRun = vi.fn()
    render(
      <SystemStatusBubble
        run={run('failed')}
        onCancelRun={onCancelRun}
        onRetryRun={onRetryRun}
      />,
    )
    expect(screen.getByRole('button', { name: /retry/i })).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: /retry/i }))
    expect(onRetryRun).toHaveBeenCalledWith('r1')
  })

  it('renders no action buttons for a succeeded run', () => {
    render(<SystemStatusBubble run={run('succeeded')} onCancelRun={vi.fn()} onRetryRun={vi.fn()} />)
    expect(screen.queryByRole('button', { name: /cancel/i })).toBeNull()
    expect(screen.queryByRole('button', { name: /retry/i })).toBeNull()
  })

  it('lists running steps with their descriptions', () => {
    render(
      <SystemStatusBubble
        run={run('running')}
        steps={[{ id: 'st1', run_id: 'r1', step_index: 1, description: 'step one', status: 'running' }]}
        onCancelRun={vi.fn()}
        onRetryRun={vi.fn()}
      />,
    )
    expect(screen.getByText('step one')).toBeTruthy()
  })
})
```

`frontend/src/components/Messages/AssistantBubble.test.tsx`:

```tsx
import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { AssistantBubble } from './AssistantBubble'
import type { Run } from '@/lib/types'

function run(status: Run['status'], resultJson?: string): Run {
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
    result_json: resultJson,
  }
}

describe('AssistantBubble', () => {
  it('renders the ResultViewBlock when result_json is present and succeeded', () => {
    render(<AssistantBubble run={run('succeeded', '{"ok":true}')} />)
    expect(screen.getByText(/Result/i)).toBeTruthy()
  })

  it('renders an error summary when the run failed', () => {
    const failingRun = { ...run('failed'), error_code: 'E_TIMEOUT', error_summary: 'timed out' } as Run
    render(<AssistantBubble run={failingRun} />)
    expect(screen.getByText(/timed out/i)).toBeTruthy()
  })

  it('renders nothing meaningful when there is no result and no error', () => {
    const { container } = render(<AssistantBubble run={run('succeeded') as Run} />)
    expect(container.textContent).toBe('')
  })
})
```

- [ ] **Step 2: Run tests to confirm they fail**

Run: `cd frontend && npx vitest run src/components/Messages/SystemStatusBubble.test.tsx src/components/Messages/AssistantBubble.test.tsx`
Expected: FAIL — modules not found

- [ ] **Step 3: Implement all six files**

`frontend/src/components/Messages/UserBubble.tsx`:

```tsx
import type { ChatMessage } from '@/lib/chat-messages'
import { cn } from '@/lib/utils'

type UserMsg = Extract<ChatMessage, { kind: 'user' }>

export function UserBubble({ message }: { message: UserMsg }) {
  const failed = message.status === 'failed'
  return (
    <div className="flex justify-end">
      <div
        className={cn(
          'max-w-[80%] whitespace-pre-wrap rounded-2xl rounded-br-sm px-4 py-2 text-sm',
          failed
            ? 'border border-rose-500/40 bg-rose-500/10 text-rose-200'
            : 'bg-primary text-primary-foreground',
        )}
      >
        <p>{message.goal}</p>
        {message.status === 'pending' && (
          <span className="mt-1 block text-[11px] opacity-70">Sending…</span>
        )}
        {failed && (
          <span className="mt-1 block text-[11px]">
            Failed: {message.error ?? 'unknown error'}
          </span>
        )}
      </div>
    </div>
  )
}
```

`frontend/src/components/Messages/AssistantBubble.tsx`:

```tsx
import { Run } from '@/lib/types'
import { ResultViewBlock } from '@/lib/result-block'

export function AssistantBubble({ run }: { run: Run }) {
  if (run.error_code) {
    return (
      <div className="rounded-lg border border-rose-500/30 bg-rose-500/10 p-3 text-sm">
        <p className="font-medium text-rose-600 dark:text-rose-400">Run failed</p>
        {run.error_summary && <p className="mt-1 text-rose-700/80 dark:text-rose-300/80">{run.error_summary}</p>}
      </div>
    )
  }
  if (run.result_json) {
    return (
      <div className="rounded-lg border border-border bg-card p-3">
        <p className="mb-2 text-xs font-medium text-muted-foreground">Result</p>
        <ResultViewBlock resultJson={run.result_json} />
      </div>
    )
  }
  return null
}
```

`frontend/src/components/Messages/SystemStatusBubble.tsx`:

```tsx
import { useState } from 'react'
import { api } from '@/lib/api'
import { useRunStore } from '@/store/run-store'
import { cn } from '@/lib/utils'
import { stepStatusClasses, statusBadgeClass } from '@/lib/status'
import type { Run, Step } from '@/lib/types'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'

interface Props {
  run: Run
  steps?: Step[]
  onCancelRun: (runId: string) => void
  onRetryRun: (runId: string) => void
}

export function SystemStatusBubble({ run, steps = [], onCancelRun, onRetryRun }: Props) {
  const [confirmOpen, setConfirmOpen] = useState(false)
  const isActive = !['succeeded', 'failed', 'cancelled', 'timed_out'].includes(run.status)

  return (
    <div className="rounded-lg border border-border bg-card/50 p-3 text-sm">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <span className={cn('rounded-full px-2 py-0.5 text-[11px]', statusBadgeClass(run.status))}>
            {run.status.replace('_', ' ')}
          </span>
          <span className="text-xs text-muted-foreground">{run.mode}</span>
        </div>
        <div className="flex items-center gap-2">
          {isActive && (
            <Dialog open={confirmOpen} onOpenChange={setConfirmOpen}>
              <button
                type="button"
                className="rounded-md border border-border px-2 py-1 text-xs hover:bg-accent"
                onClick={() => setConfirmOpen(true)}
              >
                Cancel
              </button>
              <DialogContent className="sm:max-w-[360px]">
                <DialogHeader>
                  <DialogTitle>Cancel this run?</DialogTitle>
                  <DialogDescription>
                    The run will stop and cannot be resumed from this UI.
                  </DialogDescription>
                </DialogHeader>
                <DialogFooter>
                  <button
                    type="button"
                    className="rounded-md border border-border px-3 py-1.5 text-xs hover:bg-accent"
                    onClick={() => setConfirmOpen(false)}
                  >
                    Keep running
                  </button>
                  <button
                    type="button"
                    className="rounded-md bg-rose-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-rose-700"
                    onClick={async () => {
                      setConfirmOpen(false)
                      onCancelRun(run.id)
                      try {
                        await api.cancelRun(run.id)
                      } catch {
                        // error surfaced by global api error handling
                      }
                    }}
                  >
                    Cancel run
                  </button>
                </DialogFooter>
              </DialogContent>
            </Dialog>
          )}
          {run.status === 'failed' || run.status === 'cancelled' || run.status === 'timed_out' ? (
            <button
              type="button"
              className="rounded-md border border-border px-2 py-1 text-xs hover:bg-accent"
              onClick={() => {
                onRetryRun(run.id)
                void api.resumeRun(run.id).catch(() => {})
              }}
            >
              Retry
            </button>
          ) : null}
        </div>
      </div>
      <p className="mt-2 text-xs text-foreground/90">{run.goal}</p>
      {steps.length > 0 && (
        <ol className="mt-2 space-y-1">
          {steps.map((s) => (
            <li key={s.id} className="flex items-center gap-2 text-xs">
              <span className={cn('h-2 w-2 rounded-full', stepStatusClasses(s.status))} />
              <span className="text-muted-foreground">{s.description}</span>
            </li>
          ))}
        </ol>
      )}
    </div>
  )
}
```

`frontend/src/components/Messages/SystemEventsBubble.tsx`:

```tsx
import type { ChatMessage } from '@/lib/chat-messages'
import { eventCategory, eventCategoryClasses, formatTimestamp, truncate } from '@/lib/status'
import { cn } from '@/lib/utils'

type EventsMsg = Extract<ChatMessage, { kind: 'system-events' }>

export function SystemEventsBubble({ message }: { message: EventsMsg }) {
  return (
    <details
      className="rounded-lg border border-border bg-card/40 text-xs"
      open={message.collapsed ? false : true}
    >
      <summary className="cursor-pointer select-none px-3 py-2 text-[11px] font-medium text-muted-foreground hover:text-foreground">
        {message.events.length} events
      </summary>
      <ol className="divide-y divide-border/50">
        {message.events.map((e, i) => (
          <li key={`${e.seq}-${i}`} className="flex items-center gap-2 px-3 py-1.5">
            <span
              className={cn(
                'rounded-full px-1.5 py-0.5 text-[10px]',
                eventCategoryClasses(eventCategory(e.event_type)),
              )}
            >
              {e.event_type.replace('.', ' ')}
            </span>
            <span className="flex-1 truncate text-muted-foreground">
              {truncate(e.payload_json || '', 80)}
            </span>
            <span className="text-[10px] text-muted-foreground/60">{formatTimestamp(e.created_at)}</span>
          </li>
        ))}
      </ol>
    </details>
  )
}
```

`frontend/src/components/Messages/ApprovalBubble.tsx`:

```tsx
import { api } from '@/lib/api'
import { useRunStore } from '@/store/run-store'
import { ApprovalCard } from '@/components/ApprovalCard'
import type { ChatMessage } from '@/lib/chat-messages'

type ApprovalMsg = Extract<ChatMessage, { kind: 'system-approval' }>

export function ApprovalBubble({ message }: { message: ApprovalMsg }) {
  const fetchRunAndSteps = useRunStore((s) => s.fetchRunAndSteps)

  if (message.state === 'pending') {
    return (
      <ApprovalCard
        event={message.event}
        state="pending"
        onApprove={() => {
          void api
            .decideApproval(message.runId, approvalId(message.event), true)
            .then(() => fetchRunAndSteps(message.runId))
            .catch(() => {})
        }}
        onReject={(reason) => {
          void api
            .decideApproval(message.runId, approvalId(message.event), false, reason)
            .then(() => fetchRunAndSteps(message.runId))
            .catch(() => {})
        }}
      />
    )
  }
  return (
    <ApprovalCard event={message.event} state={message.state} onApprove={() => {}} onReject={() => {}} />
  )
}

function approvalId(event: ApprovalMsg['event']): string {
  try {
    const p = JSON.parse(event.payload_json || '{}')
    return (p.approval_id as string) ?? (p.approvalId as string) ?? ''
  } catch {
    return ''
  }
}
```

`frontend/src/components/Messages/index.ts`:

```ts
export { UserBubble } from './UserBubble'
export { AssistantBubble } from './AssistantBubble'
export { SystemStatusBubble } from './SystemStatusBubble'
export { SystemEventsBubble } from './SystemEventsBubble'
export { ApprovalBubble } from './ApprovalBubble'
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd frontend && npx vitest run src/components/Messages/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/Messages/
git commit -m "feat: add chat message bubble components (User/Assistant/SystemStatus/SystemEvents/Approval)"
```

---

### Task 7: `ChatMessageList`

**Files:**
- Create: `frontend/src/components/ChatMessageList.tsx`
- Create: `frontend/src/components/ChatMessageList.test.tsx`
- Modify: `frontend/src/store/run-store.ts` (add a `resumeRun` helper that `onRetryRun` calls — or call `api.resumeRun` directly in `SystemStatusBubble` as in Task 6 Step 3; `onRetryRun` just triggers a re-activate so the chat panel updates. The simplest approach: `onRetryRun(runId)` calls `useRunStore.getState().activateRun(runId)` to refetch state after the resume call kicked off)

- [ ] **Step 1: Write failing tests**

```tsx
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { ChatMessageList } from '@/components/ChatMessageList'
import { useRunStore } from '@/store/run-store'
import { api } from '@/lib/api'
import type { Run, AgentEvent, Step, PendingGoal } from '@/lib/types'

function mkRun(status: Run['status'], extra: Partial<Run> = {}): Run {
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
    ...extra,
  }
}

function seedStores(activeRun: Run | null, activeSteps: Step[], events: AgentEvent[], pendingGoals: PendingGoal[]) {
  useRunStore.setState({
    activeRun,
    activeSteps,
    events,
    pendingGoals,
    runHistory: activeRun ? [activeRun] : [],
  })
}

describe('ChatMessageList', () => {
  it('renders user, system-status, assistant bubbles in order for a succeeded run', () => {
    const run = mkRun('succeeded', { result_json: '{"ok":true}' })
    seedStores(run, [], [], [])
    render(<ChatMessageList />)
    expect(screen.getByText('do thing')).toBeTruthy()
    expect(screen.getByText(/Result/i)).toBeTruthy()
  })

  it('renders a pending user bubble for pendingGoals and no run', () => {
    seedStores(null, [], [], [{ id: 'p1', goal: 'waiting', status: 'pending' }])
    render(<ChatMessageList />)
    expect(screen.getByText('waiting')).toBeTruthy()
    expect(screen.getByText(/Sending/i)).toBeTruthy()
  })

  it('renders approval bubble wired to api.decideApproval', async () => {
    const decide = vi.fn().mockResolvedValue({ status: 'decided' })
    vi.mocked(api.decideApproval) = decide
    const run = mkRun('waiting_approval')
    const approvalEvent: AgentEvent = {
      seq: 1,
      run_id: 'r1',
      event_type: 'approval.requested',
      payload_json: JSON.stringify({ approval_id: 'a1', tool_name: 'shell' }),
      sensitivity: 'normal',
      created_at: 0,
    }
    seedStores(run, [], [approvalEvent], [])
    render(<ChatMessageList />)
    fireEvent.click(screen.getByRole('button', { name: /approve/i }))
    await vi.waitFor(() =>
      expect(decide).toHaveBeenCalledWith('r1', 'a1', true, undefined),
    )
  })
})
```

- [ ] **Step 2: Run tests to confirm they fail**

Run: `cd frontend && npx vitest run src/components/ChatMessageList.test.tsx`
Expected: FAIL — module not found

- [ ] **Step 3: Implement ChatMessageList**

```tsx
import { useMemo } from 'react'
import { deriveMessages, type ChatMessage } from '@/lib/chat-messages'
import { groupEventsByRun } from '@/store/selectors'
import { useRunStore } from '@/store/run-store'
import { UserBubble } from '@/components/Messages/UserBubble'
import { AssistantBubble } from '@/components/Messages/AssistantBubble'
import { SystemStatusBubble } from '@/components/Messages/SystemStatusBubble'
import { SystemEventsBubble } from '@/components/Messages/SystemEventsBubble'
import { ApprovalBubble } from '@/components/Messages/ApprovalBubble'

export function ChatMessageList() {
  const runHistory = useRunStore((s) => s.runHistory)
  const activeRun = useRunStore((s) => s.activeRun)
  const activeSteps = useRunStore((s) => s.activeSteps)
  const events = useRunStore((s) => s.events)
  const pendingGoals = useRunStore((s) => s.pendingGoals)
  const activateRun = useRunStore((s) => s.activateRun)

  const eventsByRun = useMemo(() => groupEventsByRun(events), [events])

  const messages = useMemo(
    () =>
      deriveMessages({
        runHistory,
        activeRun,
        activeSteps,
        eventsByRun,
        pendingGoals,
      }),
    [runHistory, activeRun, activeSteps, eventsByRun, pendingGoals],
  )

  const handleRetry = (runId: string) => {
    void activateRun(runId)
  }

  return (
    <div className="flex flex-col gap-3">
      {messages.map((m) => (
        <Bubble key={m.id} message={m} onRetry={handleRetry} />
      ))}
    </div>
  )
}

function Bubble({
  message,
  onRetry,
}: {
  message: ChatMessage
  onRetry: (runId: string) => void
}) {
  switch (message.kind) {
    case 'user':
      return <UserBubble message={message} />
    case 'assistant':
      // assistant carries the run inline in the derived shape? No — the type
      // currently only carries resultJson/error/runId. Look up the run for the
      // full object.
      return <AssistantForRun runId={message.runId} resultJson={message.resultJson} error={message.error} onRetry={onRetry} />
    case 'system-status':
      return (
        <SystemStatusBubble
          run={activeRunById(message.runId)}
          steps={message.steps}
          onCancelRun={(id) => void 0}
          onRetryRun={onRetry}
        />
      )
    case 'system-events':
      return <SystemEventsBubble message={message} />
    case 'system-approval':
      return <ApprovalBubble message={message} />
    default:
      return null
  }
}
```

Note: the `assistant` / `system-status` bubbles need the full `Run` object. Extend `ChatMessage` in `lib/chat-messages.ts` to carry `run: Run` on the `assistant` and `system-status` variants instead of just the individual fields. Add to Task 2's type definition:

```ts
| {
    kind: 'assistant'
    id: string
    runId: string
    run: Run
    resultJson?: string
    error?: { code: string; summary?: string }
  }
| {
    kind: 'system-status'
    id: string
    runId: string
    run: Run
    status: Run['status']
    mode: string
    goal: string
    steps: Step[]
    budget: Budget
    error?: { code: string; summary?: string }
  }
```

and update `emitRun` in `chat-messages.ts` to pass `run` through. Update the corresponding tests in Task 2 to also assert `m.run` is present (add one assertion: `expect((out.find(m => m.kind === 'assistant') as any).run).toBe(run)`). Re-run Task 2's test file after this change and keep it green.

Simplify the `Bubble` switch accordingly:

```tsx
case 'assistant':
  return <AssistantBubble run={message.run} />
case 'system-status':
  return (
    <SystemStatusBubble
      run={message.run}
      steps={message.steps}
      onCancelRun={() => {}}
      onRetryRun={onRetry}
    />
  )
```

Move `onCancelRun` to a direct `api.cancelRun(message.run.id)` call inside `SystemStatusBubble` (as already written in Task 6) and drop the `onCancelRun` prop from `SystemStatusBubble`'s signature; keep `onRetryRun` since the list needs to trigger `activateRun` for UI refresh.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd frontend && npx vitest run src/components/ChatMessageList.test.tsx`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add frontend/src/lib/chat-messages.ts frontend/src/lib/chat-messages.test.ts frontend/src/components/ChatMessageList.tsx frontend/src/components/ChatMessageList.test.tsx
git commit -m "feat: add ChatMessageList deriving bubbles from deriveMessages"
```

---

### Task 8: `ChatInputBar`

**Files:**
- Create: `frontend/src/components/ChatInputBar.tsx`
- Create: `frontend/src/components/ChatInputBar.test.tsx`

- [ ] **Step 1: Write failing tests**

```tsx
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { ChatInputBar } from '@/components/ChatInputBar'
import { useSessionStore } from '@/store/session-store'
import { useRunStore } from '@/store/run-store'
import * as apiModule from '@/lib/api'

describe('ChatInputBar', () => {
  it('submits goal via submitRun on Enter when goal is non-empty', async () => {
    const submitRun = vi.fn().mockResolvedValue(undefined)
    useRunStore.setState({ submitRun, activeRun: null })
    vi.mocked(apiModule.api.createRun) = vi.fn().mockResolvedValue({ run_id: 'r1', status: 'queued', duplicated: false })
    useSessionStore.setState({ activeSessionId: 's1' })

    render(<ChatInputBar />)
    const textarea = screen.getByRole('textbox') as HTMLTextAreaElement
    fireEvent.change(textarea, { target: { value: 'hello' } })
    fireEvent.keyDown(textarea, { key: 'Enter', ctrlKey: true })
    expect(submitRun).toHaveBeenCalledWith(expect.objectContaining({ goal: 'hello', sessionId: 's1' }))
  })

  it('does not submit when goal is blank', () => {
    const submitRun = vi.fn()
    useRunStore.setState({ submitRun, activeRun: null })
    useSessionStore.setState({ activeSessionId: 's1' })
    render(<ChatInputBar />)
    const textarea = screen.getByRole('textbox') as HTMLTextAreaElement
    fireEvent.keyDown(textarea, { key: 'Enter', ctrlKey: true })
    expect(submitRun).not.toHaveBeenCalled()
  })

  it('opens the NewRunDialog via the advanced-settings gear button', () => {
    useRunStore.setState({ submitRun: vi.fn(), activeRun: null })
    useSessionStore.setState({ activeSessionId: 's1' })
    render(<ChatInputBar />)
    const gear = screen.getByRole('button', { name: /advanced|settings|gear/i })
    fireEvent.click(gear)
    expect(screen.getByRole('dialog')).toBeTruthy()
  })
})
```

- [ ] **Step 2: Run tests to confirm they fail**

Run: `cd frontend && npx vitest run src/components/ChatInputBar.test.tsx`
Expected: FAIL — module not found

- [ ] **Step 3: Implement ChatInputBar**

```tsx
import { useState } from 'react'
import { useSessionStore } from '@/store/session-store'
import { useRunStore } from '@/store/run-store'
import { NewRunDialog } from '@/components/NewRunDialog'

export function ChatInputBar() {
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const submitRun = useRunStore((s) => s.submitRun)
  const pendingGoals = useRunStore((s) => s.pendingGoals)
  const [goal, setGoal] = useState('')
  const [dialogOpen, setDialogOpen] = useState(false)

  const busy = pendingGoals.some((p) => p.status === 'pending')

  const submit = () => {
    const trimmed = goal.trim()
    if (!trimmed || busy) return
    setGoal('')
    void submitRun({
      sessionId: activeSessionId,
      goal: trimmed,
      mode: undefined,
      constraints: undefined,
      successCriteria: undefined,
      budget: undefined,
    })
  }

  return (
    <div className="border-t border-border bg-background/80 p-3 backdrop-blur">
      <div className="flex items-end gap-2">
        <textarea
          className="max-h-40 min-h-[3rem] flex-1 resize-y rounded-lg border border-border bg-card p-3 text-sm focus:outline-none focus:ring-2 focus:ring-ring/40"
          placeholder="Describe the task for this session…"
          rows={1}
          value={goal}
          onChange={(e) => setGoal(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && (e.ctrlKey || e.metaKey) && !e.shiftKey) {
              e.preventDefault()
              submit()
            }
          }}
        />
        <button
          type="button"
          title="Advanced settings"
          aria-label="Advanced settings"
          className="rounded-lg border border-border p-2.5 text-muted-foreground hover:bg-accent hover:text-foreground"
          onClick={() => setDialogOpen(true)}
        >
          ⚙
        </button>
        <button
          type="button"
          className="rounded-lg bg-primary px-4 py-2.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
          disabled={busy || !goal.trim()}
          onClick={submit}
        >
          Send
        </button>
      </div>
      <NewRunDialog open={dialogOpen} onOpenChange={setDialogOpen} />
    </div>
  )
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd frontend && npx vitest run src/components/ChatInputBar.test.tsx`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/ChatInputBar.tsx frontend/src/components/ChatInputBar.test.tsx
git commit -m "feat: add ChatInputBar with quick goal input + advanced settings gear"
```

---

### Task 9: `NewRunDialog` (migrated from RunForm advanced fields)

**Files:**
- Create: `frontend/src/components/NewRunDialog.tsx`
- Create: `frontend/src/components/NewRunDialog.test.tsx`
- (No deletion yet — `RunForm.tsx` is deleted in Task 12)

- [ ] **Step 1: Write failing tests**

```tsx
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/react'
import { NewRunDialog } from '@/components/NewRunDialog'
import { useSessionStore } from '@/store/session-store'
import { useRunStore } from '@/store/run-store'
import * as apiModule from '@/lib/api'

describe('NewRunDialog', () => {
  it('submits full CreateRunReq with budget + constraints + success criteria via submitRun', async () => {
    const submitRun = vi.fn().mockResolvedValue(undefined)
    useRunStore.setState({ submitRun })
    useSessionStore.setState({ activeSessionId: 's1' })
    vi.mocked(apiModule.api.createRun) = vi.fn().mockResolvedValue({ run_id: 'r1', status: 'queued', duplicated: false })

    render(<NewRunDialog open onOpenChange={() => {}} />)
    const dialog = screen.getByRole('dialog')

    fireEvent.change(within(dialog).getByPlaceholderText(/goal/i), { target: { value: 'do the thing' } })
    // add a constraint
    const addConstraintBtn = within(dialog).getByRole('button', { name: /add constraint/i })
    fireEvent.click(addConstraintBtn)
    const constraintInput = within(dialog).getAllByRole('textbox').find((el) =>
      (el as HTMLInputElement).placeholder?.match(/constraint/i),
    )
    if (constraintInput) {
      fireEvent.change(constraintInput, { target: { value: 'no rm -rf' } })
    }
    // set a budget value
    const maxDur = within(dialog).getByDisplayValue('') // too fragile; instead:
    // find by label
    const budgetInput = within(dialog)
      .getAllByRole('textbox')
      .find((el) => (el as HTMLInputElement).placeholder?.match(/seconds/i))
    if (budgetInput) fireEvent.change(budgetInput, { target: { value: '120' } })

    fireEvent.click(within(dialog).getByRole('button', { name: /create run|submit/i }))

    await vi.waitFor(() => {
      expect(submitRun).toHaveBeenCalledWith(
        expect.objectContaining({
          sessionId: 's1',
          goal: 'do the thing',
          budget: expect.objectContaining({ max_duration_seconds: 120 }),
        }),
      )
    })
  })

  it('omits budget when no budget fields are filled', async () => {
    const submitRun = vi.fn().mockResolvedValue(undefined)
    useRunStore.setState({ submitRun })
    useSessionStore.setState({ activeSessionId: 's1' })

    render(<NewRunDialog open onOpenChange={() => {}} />)
    const dialog = screen.getByRole('dialog')
    fireEvent.change(within(dialog).getByPlaceholderText(/goal/i), { target: { value: 'minimal' } })
    fireEvent.click(within(dialog).getByRole('button', { name: /create run|submit/i }))

    await vi.waitFor(() => {
      const arg = submitRun.mock.calls[0][0]
      expect(arg.goal).toBe('minimal')
      expect(arg.budget).toBeUndefined()
    })
  })
})
```

- [ ] **Step 2: Run tests to confirm they fail**

Run: `cd frontend && npx vitest run src/components/NewRunDialog.test.tsx`
Expected: FAIL — module not found

- [ ] **Step 3: Implement NewRunDialog (migrating the full RunForm field set)**

The content below is the RunForm's internal fields (mode/constraints/successCriteria/budget, per the spec decision "极简输入 + 高级弹窗") wrapped in a Radix `Dialog` so it can be triggered from `ChatInputBar`'s gear button, from `SessionList`'s "New" button when no session exists yet, and from `SessionList`'s "Advanced" button when a session is active. It reuses `BUDGET_FIELDS` + `assembleBudget` from `lib/budget.ts` and `submitRun` from the store.

```tsx
import { useState } from 'react'
import { useSessionStore } from '@/store/session-store'
import { useRunStore } from '@/store/run-store'
import { assembleBudget, BUDGET_FIELDS, type BudgetKey } from '@/lib/budget'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function NewRunDialog({ open, onOpenChange }: Props) {
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const submitRun = useRunStore((s) => s.submitRun)
  const [goal, setGoal] = useState('')
  const [mode, setMode] = useState<'react' | 'plan_execute'>('react')
  const [constraints, setConstraints] = useState<string[]>([])
  const [successCriteria, setSuccessCriteria] = useState<string[]>([])
  const [budgetInput, setBudgetInput] = useState<Partial<Record<BudgetKey, string>>>({})

  const canSubmit = goal.trim().length > 0

  const submit = () => {
    if (!canSubmit || !activeSessionId) return
    const cleanConstraints = constraints.map((c) => c.trim()).filter(Boolean)
    const cleanSuccess = successCriteria.map((c) => c.trim()).filter(Boolean)
    void submitRun({
      sessionId: activeSessionId,
      goal: goal.trim(),
      mode,
      constraints: cleanConstraints.length > 0 ? cleanConstraints : undefined,
      successCriteria: cleanSuccess.length > 0 ? cleanSuccess : undefined,
      budget: assembleBudget(budgetInput),
    })
    setGoal('')
    setConstraints([])
    setSuccessCriteria([])
    setBudgetInput({})
    onOpenChange(false)
  }

  const addList = (setter: (updater: (prev: string[]) => string[]) => void, value: string) => {
    setter((prev) => [...prev, value])
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-[560px]">
        <DialogHeader>
          <DialogTitle>New Run</DialogTitle>
          <DialogDescription>
            Advanced options for the next run in this session.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          <div className="space-y-1.5">
            <label className="text-xs font-medium">Goal</label>
            <textarea
              className="w-full rounded-lg border border-border bg-card p-2.5 text-sm focus:outline-none focus:ring-2 focus:ring-ring/40"
              rows={3}
              placeholder="Describe the goal…"
              value={goal}
              onChange={(e) => setGoal(e.target.value)}
            />
          </div>

          <div className="space-y-1.5">
            <label className="text-xs font-medium">Mode</label>
            <div className="flex gap-2">
              {(['react', 'plan_execute'] as const).map((m) => (
                <button
                  key={m}
                  type="button"
                  className={
                    'rounded-md border px-3 py-1.5 text-xs ' +
                    (mode === m
                      ? 'border-primary bg-primary/10 font-medium text-primary'
                      : 'border-border text-muted-foreground hover:bg-accent')
                  }
                  onClick={() => setMode(m)}
                >
                  {m === 'react' ? 'ReAct' : 'Plan & Execute'}
                </button>
              ))}
            </div>
          </div>

          <ConstraintEditor
            label="Constraints"
            items={constraints}
            onChange={setConstraints}
            placeholder="Add a constraint (e.g. do not delete files)"
          />
          <ConstraintEditor
            label="Success criteria"
            items={successCriteria}
            onChange={setSuccessCriteria}
            placeholder="Add a success criterion"
          />

          <div className="space-y-1.5">
            <label className="text-xs font-medium">Budget (optional)</label>
            <div className="grid grid-cols-2 gap-2">
              {BUDGET_FIELDS.map((f) => (
                <div key={f.key} className="space-y-0.5">
                  <label className="text-[11px] text-muted-foreground">
                    {f.label} <span className="opacity-60">{f.hint}</span>
                  </label>
                  <input
                    className="w-full rounded-md border border-border bg-card px-2 py-1.5 text-xs"
                    inputMode="numeric"
                    placeholder="—"
                    value={budgetInput[f.key] ?? ''}
                    onChange={(e) =>
                      setBudgetInput((prev) => ({ ...prev, [f.key]: e.target.value }))
                    }
                  />
                </div>
              ))}
            </div>
          </div>
        </div>

        <DialogFooter>
          <button
            type="button"
            className="rounded-md border border-border px-3 py-1.5 text-xs hover:bg-accent"
            onClick={() => onOpenChange(false)}
          >
            Cancel
          </button>
          <button
            type="button"
            className="rounded-md bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
            disabled={!canSubmit}
            onClick={submit}
          >
            Create run
          </button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function ConstraintEditor({
  label,
  items,
  onChange,
  placeholder,
}: {
  label: string
  items: string[]
  onChange: (updater: (prev: string[]) => string[]) => void
  placeholder: string
}) {
  const [draft, setDraft] = useState('')
  return (
    <div className="space-y-1.5">
      <label className="text-xs font-medium">{label}</label>
      {items.length > 0 && (
        <ul className="space-y-1">
          {items.map((item, i) => (
            <li key={i} className="flex items-center gap-2 rounded-md border border-border bg-card px-2 py-1 text-xs">
              <span className="flex-1">{item}</span>
              <button
                type="button"
                aria-label={`remove ${label} ${i + 1}`}
                className="text-muted-foreground hover:text-foreground"
                onClick={() => onChange((prev) => prev.filter((_, j) => j !== i))}
              >
                ✕
              </button>
            </li>
          ))}
        </ul>
      )}
      <div className="flex gap-2">
        <input
          className="flex-1 rounded-md border border-border bg-card px-2 py-1.5 text-xs"
          placeholder={placeholder}
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && draft.trim()) {
              onChange((prev) => [...prev, draft.trim()])
              setDraft('')
            }
          }}
        />
        <button
          type="button"
          className="rounded-md border border-border px-2 py-1.5 text-xs hover:bg-accent disabled:opacity-50"
          disabled={!draft.trim()}
          onClick={() => {
            onChange((prev) => [...prev, draft.trim()])
            setDraft('')
          }}
        >
          Add
        </button>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd frontend && npx vitest run src/components/NewRunDialog.test.tsx`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/NewRunDialog.tsx frontend/src/components/NewRunDialog.test.tsx
git commit -m "feat: add NewRunDialog with mode/constraints/success criteria/budget advanced fields"
```

---

### Task 10: `SessionList` (slim sidebar replacing SessionPanel)

**Files:**
- Create: `frontend/src/components/SessionList.tsx`
- Create: `frontend/src/components/SessionList.test.tsx`
- Modify: `frontend/src/store/ui-store.ts` (remove `eventStreamOpen` / `toggleEventStream`)
- Modify: `frontend/src/store/ui-store.test.ts` (remove those two fields from the reset + add a note that they no longer exist)

- [ ] **Step 1: Write failing tests**

```tsx
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { SessionList } from '@/components/SessionList'
import { useSessionStore } from '@/store/session-store'
import * as apiModule from '@/lib/api'

describe('SessionList', () => {
  it('renders session names and highlights the active one', () => {
    useSessionStore.setState({
      sessions: [
        { id: 's1', name: 'Alpha', created_at: 1, updated_at: 1 },
        { id: 's2', name: 'Beta', created_at: 1, updated_at: 1 },
      ],
      activeSessionId: 's1',
    })
    render(<SessionList />)
    expect(screen.getByText('Alpha').className).toMatch(/border-primary|ring|bg-primary|font-medium|active/i)
  })

  it('switches session on click', () => {
    const switchSession = vi.fn().mockResolvedValue(undefined)
    useSessionStore.setState({
      sessions: [{ id: 's2', name: 'Beta', created_at: 1, updated_at: 1 }],
      activeSessionId: 's1',
      switchSession,
    })
    render(<SessionList />)
    fireEvent.click(screen.getByText('Beta'))
    expect(switchSession).toHaveBeenCalledWith('s2')
  })

  it('shows a "New" button that opens NewRunDialog', () => {
    useSessionStore.setState({ sessions: [], activeSessionId: null, createSession: vi.fn() })
    render(<SessionList />)
    const newBtn = screen.getByRole('button', { name: /new session/i })
    fireEvent.click(newBtn)
    expect(screen.getByRole('dialog')).toBeTruthy()
  })
})
```

- [ ] **Step 2: Run tests to confirm they fail**

Run: `cd frontend && npx vitest run src/components/SessionList.test.tsx`
Expected: FAIL — module not found

- [ ] **Step 3: Implement SessionList**

```tsx
import { useState } from 'react'
import { useSessionStore } from '@/store/session-store'
import { NewRunDialog } from '@/components/NewRunDialog'
import { cn } from '@/lib/utils'

export function SessionList() {
  const sessions = useSessionStore((s) => s.sessions)
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const switchSession = useSessionStore((s) => s.switchSession)
  const createSession = useSessionStore((s) => s.createSession)
  const [dialogOpen, setDialogOpen] = useState(false)

  return (
    <aside className="flex h-full w-64 shrink-0 flex-col border-r border-border bg-card/30">
      <div className="flex items-center justify-between px-3 py-2.5">
        <h2 className="text-sm font-semibold">Sessions</h2>
        <button
          type="button"
          className="rounded-md border border-border px-2 py-1 text-xs hover:bg-accent"
          onClick={async () => {
            await createSession()
            setDialogOpen(true)
          }}
        >
          New
        </button>
      </div>
      <nav className="flex-1 overflow-y-auto px-2 pb-2">
        <ul className="space-y-1">
          {sessions.map((s) => (
            <li key={s.id}>
              <button
                type="button"
                className={cn(
                  'w-full rounded-md px-2.5 py-1.5 text-left text-sm',
                  s.id === activeSessionId
                    ? 'bg-primary/10 font-medium text-primary'
                    : 'text-foreground hover:bg-accent',
                )}
                onClick={() => void switchSession(s.id)}
              >
                {s.name}
              </button>
            </li>
          ))}
          {sessions.length === 0 && (
            <li className="px-2.5 py-2 text-xs text-muted-foreground">No sessions yet</li>
          )}
        </ul>
      </nav>
      <NewRunDialog open={dialogOpen} onOpenChange={setDialogOpen} />
    </aside>
  )
}
```

- [ ] **Step 4: Remove `eventStreamOpen`/`toggleEventStream` from ui-store**

In `frontend/src/store/ui-store.ts`:
- Remove `eventStreamOpen: boolean` from the state interface
- Remove `toggleEventStream: () => void` from the state interface
- Remove both from the initial state object
- Update `frontend/src/store/ui-store.test.ts` to drop the two related assertions/fields

Run: `cd frontend && npx vitest run src/store/ui-store.test.ts`
Expected: PASS after the removal

- [ ] **Step 5: Run all Chunk 2 tests**

Run: `cd frontend && npx vitest run src/components/SessionList.test.tsx src/store/ui-store.test.ts`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add frontend/src/components/SessionList.tsx frontend/src/components/SessionList.test.tsx frontend/src/store/ui-store.ts frontend/src/store/ui-store.test.ts
git commit -m "feat: add slim SessionList sidebar, remove eventStreamOpen from ui-store"
```

---

## Chunk 3: Wiring — App rewrite, delete legacy components + tests, end-to-end verification

### Task 11: Rewrite `App.tsx` to the chat layout

**Files:**
- Modify: `frontend/src/App.tsx`

- [ ] **Step 1: Rewrite App.tsx**

```tsx
import { useEffect } from 'react'
import { useSessionStore } from '@/store/session-store'
import { useRunStore } from '@/store/run-store'
import { useEventStream } from '@/hooks/use-event-stream'
import { Header } from '@/components/Header'
import { SessionList } from '@/components/SessionList'
import { ChatMessageList } from '@/components/ChatMessageList'
import { ChatInputBar } from '@/components/ChatInputBar'

export default function App() {
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const loadSessions = useSessionStore((s) => s.loadSessions)
  const loadRunHistory = useRunStore((s) => s.loadRunHistory)

  useEventStream()

  useEffect(() => {
    void loadSessions()
  }, [loadSessions])

  useEffect(() => {
    if (activeSessionId) void loadRunHistory(activeSessionId)
  }, [activeSessionId, loadRunHistory])

  return (
    <div className="flex h-screen flex-col">
      <Header />
      <div className="flex flex-1 overflow-hidden">
        <SessionList />
        <main className="flex flex-1 flex-col overflow-hidden">
          <div className="flex-1 overflow-y-auto p-4">
            <ChatMessageList />
          </div>
          <ChatInputBar />
        </main>
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Run type check**

Run: `cd frontend && npx tsc --noEmit`
Expected: PASS (RunForm/RunDetail/EventStream/ActionBar are still referenced only in their own files and tests, which Task 12 deletes)

- [ ] **Step 3: Commit**

```bash
git add frontend/src/App.tsx
git commit -m "feat: rewire App.tsx to chat layout (SessionList + ChatMessageList + ChatInputBar)"
```

---

### Task 12: Delete legacy components + their tests

**Files:**
- Delete: `frontend/src/components/RunForm.tsx`
- Delete: `frontend/src/components/RunForm.test.tsx`
- Delete: `frontend/src/components/RunDetail.tsx`
- Delete: `frontend/src/components/RunDetail.test.tsx`
- Delete: `frontend/src/components/EventStream.tsx`
- Delete: `frontend/src/components/EventStream.test.tsx`
- Delete: `frontend/src/components/ActionBar.tsx`
- Delete: `frontend/src/components/ActionBar.test.tsx`
- Delete: `frontend/src/components/SessionPanel.tsx`
- Delete: `frontend/src/components/SessionPanel.test.tsx`

- [ ] **Step 1: Confirm no remaining references**

Run:
```bash
grep -r "RunForm\|RunDetail\|EventStream\|ActionBar\|SessionPanel" frontend/src --include='*.tsx' --include='*.ts'
```
Expected: zero matches after deletion. Fix any lingering references (e.g. a stray import in `Header.tsx` referencing `eventStreamOpen` from ui-store — remove that usage or replace with a static indicator).

- [ ] **Step 2: Delete the files**

```bash
git rm frontend/src/components/RunForm.tsx frontend/src/components/RunForm.test.tsx \
  frontend/src/components/RunDetail.tsx frontend/src/components/RunDetail.test.tsx \
  frontend/src/components/EventStream.tsx frontend/src/components/EventStream.test.tsx \
  frontend/src/components/ActionBar.tsx frontend/src/components/ActionBar.test.tsx \
  frontend/src/components/SessionPanel.tsx frontend/src/components/SessionPanel.test.tsx
```

- [ ] **Step 3: Run full type check + full test suite**

Run: `cd frontend && npx tsc --noEmit && npx vitest run`
Expected: PASS (all remaining tests green; deleted component tests no longer run)

- [ ] **Step 4: Commit**

```bash
git commit -m "chore: delete RunForm/RunDetail/EventStream/ActionBar/SessionPanel superseded by chat layout"
```

---

### Task 13: End-to-end verification

**Files:** none (verification only)

- [ ] **Step 1: Start the full stack**

Run the backend + frontend per the repo's standard dev commands (check `README.md` / `Makefile` for the exact commands; likely `make dev` or two processes).

- [ ] **Step 2: Verify each spec acceptance criterion**

1. Send a simple goal from `ChatInputBar` (no advanced dialog) → user bubble appears optimistically with "Sending…", then transitions to system-status + assistant bubble on terminal state
2. Open the gear → `NewRunDialog` → fill budget fields → submit → budget is reflected in the system-status bubble's budget row
3. Trigger a run that requires approval (existing backend tool approval flow) → `ApprovalBubble` appears inline; click Approve → run continues; the bubble transitions to "Approved" after `approval.decided` SSE event arrives
4. Click a different session in `SessionList` → `ChatMessageList` re-renders with that session's history (via `loadRunHistory`)
5. Kill the SSE stream (or disconnect network) → reconnect banner appears (existing `useEventStream` behavior), reconnecting replays from `Last-Event-ID`
6. Cancel a running run from the system-status bubble → run transitions to `cancelled`, assistant bubble shows "cancelled" state
7. Refresh the page on an active session → history reconstructs via `loadRunHistory` + `fetchRunAndSteps`, no data loss

- [ ] **Step 3: Final commit (if any fixes were made during verification)**

```bash
git add -A && git commit -m "fix: chat dialog interaction end-to-end fixes"
```
