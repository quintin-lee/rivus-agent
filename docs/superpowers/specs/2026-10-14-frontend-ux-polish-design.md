# Frontend UX Polish — Stop Button, Approval Collapse, Event Fallbacks, Empty States

- **Date**: 2026-10-14
- **Scope**: `frontend/` only. No backend changes.
- **Status**: Design approved by user (3 sections, all confirmed).
- **Approach**: Option A — incremental in-place fixes on existing components; no new source modules.

## Background

The chat UI (Phase 1, `2026-10-10-chat-dialog-interaction-design.md`) works end-to-end:
session list → submit → SSE events → bubbles. A UX audit found six gaps:

1. No Stop button in the input bar; cancel is buried in the per-run status bubble.
2. Results are not streamed (out of scope this round — needs backend budget events).
3. Running status bubble is static (out of scope this round).
4. Unknown/ambiguous event types render raw names or indistinguishable text.
5. Decided approvals remain as full cards in the chat.
6. Unhandled states: empty session, `cancelled`/`timed_out` runs without result render `null`.

This round fixes 1, 4, 5, 6. Gaps 2–3 are deferred to a follow-up iteration.

## Changes

### 1. Stop button (`components/ChatInputBar.tsx`)

- Read `activeRun` from `run-store`; when the active run is non-terminal
  (`isActiveRunStatus`), the Send button is replaced **in place** by a Stop button
  (`bg-rose-500` text-white, "⏹ Stop").
- Stop click:
  1. Calls `api.cancelRun(activeRun.id)` directly — **no confirm dialog**
     (Stop is immediate self-rescue; a misfire is cheap to undo by re-submitting).
  2. Sets `cancelling: true` in `run-store` to disable the button mid-flight.
  3. On success, the existing terminal-event refetch pipeline handles the rest
     (SSE `run.cancelled` → `REFRESH` → `getRun` → terminal status → button
     returns to Send).
  4. On failure (network / 404), set `cancelError` in the store; render an inline
     one-line message under the input bar; clear it on the next Stop attempt.
- `cancelling` and `cancelError` are new `run-store` fields, reset when
  `activeRun` changes or the session changes.
- Multiple concurrent runs: Stop only targets `activeRun`; other runs remain
  visible in their status bubbles.
- Pending goals do not interact: when a pending goal is in flight, both Send and
  Stop stay disabled (existing `busy` behavior).

### 2. Approval collapse (`lib/chat-messages.ts` + `components/ApprovalCard.tsx`)

- `deriveApprovals` gains one state: if a requested approval has no
  `approval.decided` event **and** its run is in a terminal status, derive state
  `'expired'` (the run can no longer act on the approval). Otherwise
  `pending | approved | rejected` as today.
- `ApprovalCard` rendering:
  - `pending`: unchanged card (approve/reject buttons, reject-reason dialog).
  - `approved`: single neutral line `✓ Approved {tool_name}` — no card border,
    no badge block.
  - `rejected`: single line `✗ Rejected {tool_name} · {reason}` (reason from the
    `approval.decided` payload when present).
  - `expired`: single muted line `Approval expired · {tool_name}`.
- The single-line forms live in `ApprovalBubble` (or `ApprovalCard` with a
  compact render path) — implementation detail; the public shape is
  `state: 'pending' | 'approved' | 'rejected' | 'expired'` which
  `ApprovalCard` already accepts.

### 3. Event summary texts (`lib/status.ts` `eventSummary`)

Backend event vocabulary (verified by grep over `backend/internal`, 22 types):
`tool.requested/started/completed/failed/authorized/denied`,
`model.requested/completed/failed`, `step.started/finished`,
`approval.requested/decided`, `run.created/started/finished/failed/cancelled/resumed`,
`plan.created/updated`, `checkpoint.saved`, `verification.passed/failed`.

Rewrite the tool/model branches so each type is distinguishable:

| event | text |
|---|---|
| `tool.requested` | `requesting tool {tool_name}` |
| `tool.started` | `tool {tool_name} started` |
| `tool.completed` | `tool {tool_name} completed` |
| `tool.failed` | `tool {tool_name} failed` |
| `tool.authorized` | `tool {tool_name} authorized` |
| `tool.denied` | `tool {tool_name} denied` |
| `model.requested` | `model call #N started` |
| `model.completed` | `model call #N completed` |
| `model.failed` | `model call #N failed` |

- Default branch: `Unknown event {event_type}` (no raw-name leak). The existing
  raw-JSON toggle in `SystemEventsBubble` still exposes the payload.

### 4. Empty session placeholder (`components/ChatMessageList.tsx`)

When `messages` is empty (no pending goals, no runs, no active run), render a
centered placeholder: icon + "No tasks yet" + "Describe a task below to start
this session." Uses the existing `card/50` background token, no new styling.

### 5. Terminal-without-result bubbles (`components/Messages/AssistantBubble.tsx`)

| run state | no result | with result |
|---|---|---|
| `cancelled` | neutral single line "Run cancelled" | result block + top line "Cancelled — partial result" |
| `timed_out` | orange line "Run timed out" (reuses the rose/error styling pattern with orange tokens) | result block + top line "Timed out — partial result" |
| `failed` | unchanged (rose error box, existing behavior) | unchanged |
| `succeeded` | unchanged | unchanged |

`AssistantBubble` currently renders `null` when neither `error_code` nor
`result_json` is present; the cancelled/timed_out cases above close that hole.

## Data flow

No pipeline changes. Only additions:

- `run-store`: `cancelling: boolean`, `cancelError: string | null`, and the
  `cancelRun` action (which calls `api.cancelRun`, manages the two fields).
- Everything else reuses the existing SSE → `run-store.events` →
  `deriveMessages` → bubble pipeline.

```
Stop click ──► api.cancelRun(id) ──► cancelling=true (button disabled)
                              │
            success: SSE run.cancelled ──► existing REFRESH ──► getRun
                     ──► terminal status ──► Stop → Send, cancelling=false
            failure: cancelError set ──► inline line under input bar ──►
                     cancelling=false (button re-enabled for retry)
```

## Out of scope (explicit YAGNI)

- Streaming token output (needs backend SSE delta events).
- Live progress / budget consumption in the running bubble (needs backend
  budget events).
- Manual dismiss of stuck pending goals (kept as-is).
- Multi-run Stop (Stop targets `activeRun` only).

## Testing

Extend existing test files; no new source files. One new test file:
`frontend/src/lib/status.test.ts`.

| file | new cases |
|---|---|
| `components/ChatInputBar.test.tsx` | Stop rendered when activeRun non-terminal; Stop click calls `api.cancelRun`; button disabled while `cancelling`; `cancelError` renders inline |
| `lib/chat-messages.test.ts` | decided approvals keep `approved`/`rejected`; undecided approval on terminal run → `expired` |
| `components/ApprovalCard.test.tsx` (or `ApprovalBubble`) | resolved states render single-line form with no buttons; rejected shows reason; expired muted line |
| `components/Messages/AssistantBubble.test.tsx` | cancelled without result → neutral line; timed_out without result → orange line; cancelled with result → result + "Cancelled — partial result" |
| `lib/status.test.ts` (new) | each tool/model event type renders its distinct text; default returns `Unknown event {type}` |
| `components/ChatMessageList.test.tsx` | empty input renders placeholder; non-empty does not |

Acceptance: `npm test` (frontend) all green; manual smoke — submit a task,
watch Stop appear while running, click Stop, observe bubble + button return to
idle; approve a pending approval, observe the card collapse to one line.
