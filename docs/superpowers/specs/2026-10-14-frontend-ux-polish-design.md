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

### 2. Approval rendering (`lib/chat-messages.ts` + `components/ApprovalCard.tsx`)

**Payload reality (verified):** backend `approval.requested` payloads carry
`{"text":"{tool} requires approval"}` — **no `approval_id`**, no `tool_name`
key. `approval.decided` payloads carry `{"approved":true|false}` only — no
id, no reason. The domain store has a rich `domain.Approval` record
(`RunID/ToolCallID/ToolName/ArgsHash/RequestedBy/Reason/ExpiresAt`) and the
API supports `POST /api/v1/runs/{id}/approvals/{approval_id}`, but there is
**no read endpoint** exposing approvals to the frontend. So approval cards
cannot be paired today; `parseApprovalPayload` (chat-messages.ts:189) reads
`approval_id`/`tool_name` keys that never exist, the `requested` Map stays
empty, `ApprovalCard` returns `null`, and §2's feature + `expired` state
cannot fire on real data.

This round (frontend-only) ships the **rendering layer** so the
card/line layout is ready:
- `deriveApprovals` keeps reading `approval_id`/`tool_name` (harmless today:
  returns `[]` for real data, exercises the UI with synthetic test fixtures).
- `deriveApprovals` gains one derived state: if a requested approval has no
  `approval.decided` event **and** its run is in a terminal status, derive
  state `'expired'` (the run can no longer act on the approval). Otherwise
  `pending | approved | rejected`.
- `ApprovalCard` rendering:
  - `pending`: unchanged card (approve/reject buttons, reject-reason dialog).
  - `approved`: single neutral line `✓ Approved {tool}` — no card border, no
    badge block.
  - `rejected`: single line `✗ Rejected {tool}` (reason not yet available
    from events; see follow-ups).
  - `expired`: single muted line `Approval expired · {tool}`.
- The single-line forms live in `ApprovalBubble` (or `ApprovalCard` with a
  compact render path) — implementation detail; the public shape is
  `state: 'pending' | 'approved' | 'rejected' | 'expired'` which
  `ApprovalCard` already accepts.

**Follow-up (out of scope this round, needs backend):** add
`GET /api/v1/runs/{id}/approvals` returning pending approval records
(`approval_id`, `tool_name`, `expires_at`), and extend event payloads to
embed `approval_id`/`tool_name` on `approval.requested`/`approval.decided`
(tool_call_id → approval_id via the domain store). Only then can the UI pair
requested/decided, wire `api.decideApproval(approvalId)`, and render the
pending card for real runs. Tracked in `docs/superpowers/specs/2026-10-14-follow-ups.md`.

### 3. Event summary texts (`lib/status.ts` `eventSummary`)

Backend event vocabulary (verified by grep over `backend/internal`, 22 types):
`tool.requested/started/completed/failed/authorized/denied`,
`model.requested/completed/failed`, `step.started/finished`,
`approval.requested/decided`, `run.created/started/finished/failed/cancelled/resumed`,
`plan.created/updated`, `checkpoint.saved`, `verification.passed/failed`.

**Payload reality (verified):** live payloads are `{"text":…, "tool":…}`
(runtime.go:35) or `{"text":…}` (runtime.go:167) — the key is `tool`, not
`tool_name`; no `call_count` is ever emitted. The current `eventSummary`
reads `p.tool_name`/`p.call_count`, which **never match** real data, so tool
branches always fall back to the raw event_type and `model call #N` never
renders.

Rewrite the tool/model branches against the **actual** payload shape
(`p.tool`, `p.text`), keeping each type distinguishable:

| event | text (from actual payload keys) |
|---|---|
| `tool.requested` | `requesting {p.tool}` |
| `tool.started` | `{p.tool} started` |
| `tool.completed` | `{p.tool} completed` |
| `tool.failed` | `{p.tool} failed` |
| `tool.authorized` | `{p.tool} authorized` |
| `tool.denied` | `{p.tool} denied` |
| `model.requested` | `model call started` |
| `model.completed` | `model call completed` |
| `model.failed` | `model call failed` |

No `#N` — `call_count` is not in the payload. Step/approval/run/plan branches
already read the correct keys or are static; leave them, but switch the tool
and model branches from `p.tool_name`/`p.call_count` to `p.tool`/`p.text`.

- Default branch: `Unknown event {event_type}` (no raw-name leak). The
  existing raw-JSON toggle in `SystemEventsBubble` still exposes the payload.

### 4. Empty session placeholder (`components/ChatMessageList.tsx`)

When `messages` is empty (no pending goals, no runs, no active run), render a
centered placeholder: icon + "No tasks yet" + "Describe a task below to start
this session." Uses the existing `card/50` background token, no new styling.

### 5. Terminal-without-result bubbles (`components/Messages/AssistantBubble.tsx`)

**Payload reality (verified):** cancel sets `error_code="cancelled"`
(run_service.go:89) → `AssistantBubble` currently renders the rose "Run
failed" box. `RunTimedOut` is declared in the domain state machine but **no
code path sets it** (budget.go returns `ErrTimeout`; nothing maps it to
`RunTimedOut`), so the `timed_out` row is unreachable without a backend
change.

| run state | no result | with result |
|---|---|---|
| `cancelled` | neutral single line "Run cancelled" (not the rose error box) | result block + top line "Cancelled — partial result" |
| `failed` | unchanged (rose error box, existing behavior) | unchanged |
| `succeeded` | unchanged | unchanged |

The `timed_out` row is dropped from this table (unreachable; needs the
backend `ErrTimeout` → `RunTimedOut` mapping in follow-ups). The cancelled
case above closes the `null`-when-neither hole: when `error_code` is
`"cancelled"` the bubble shows the neutral line instead of the rose box.

## Data flow

No pipeline changes. Only additions:

- `run-store`: `cancelling: boolean`, `cancelError: string | null`, and the
  `cancelRun` action (which calls `api.cancelRun`, manages the two fields).
  Both fields are set `false`/`null` on **success** (before the terminal
  refetch) and on any `activeRun`/session change; `cancelling` stays
  `true` only while the HTTP call is in flight.
- Everything else reuses the existing SSE → `run-store.events` →
  `deriveMessages` → bubble pipeline.

```
Stop click ──► api.cancelRun(id) ──► cancelling=true (button disabled)
                              │
            success: cancelling=false, cancelError=null ──►
                     SSE/poller delivers run.cancelled ──► REFRESH ──► getRun
                     ──► terminal status ──► Stop → Send
            failure: cancelError set ──► inline line under input bar ──►
                     cancelling=false (button re-enabled for retry)
```

Note: the `run.cancelled` refresh reaches the UI via **either** the live SSE
stream or the 2s poller fallback (whichever is active). Both trigger the same
`REFRESH` → `getRun` path, so the Stop button's success state does not depend
on SSE being connected.

## Out of scope (explicit YAGNI / follow-ups)

Defer to `docs/superpowers/specs/2026-10-14-follow-ups.md`:

- Streaming token output (needs backend SSE delta events).
- Live progress / budget consumption in the running bubble (needs backend
  budget events).
- **Backend approval read endpoint** (`GET /runs/{id}/approvals`) + event
  payload enrichment (`approval_id`/`tool_name`) so §2's pending card and
  `api.decideApproval` can wire up for real runs.
- **Backend `ErrTimeout` → `RunTimedOut` mapping** so the timed-out bubble
  row becomes reachable (§5 drops it for this round).
- Manual dismiss of stuck pending goals (kept as-is).
- Multi-run Stop (Stop targets `activeRun` only).

## Testing

Extend existing test files; no new source files. One new test file:
`frontend/src/lib/status.test.ts`.

| file | new cases |
|---|---|
| `components/ChatInputBar.test.tsx` | Stop rendered when activeRun non-terminal; Stop click calls `api.cancelRun`; button disabled while `cancelling`; `cancelError` renders inline; `cancelling`/`cancelError` reset on success |
| `lib/chat-messages.test.ts` | decided approvals keep `approved`/`rejected`; undecided approval on terminal run → `expired`; **real-data shape** (payloads lacking `approval_id`) derives `[]` approvals, matching production behavior |
| `components/ApprovalCard.test.tsx` (or `ApprovalBubble`) | resolved states render single-line form with no buttons; rejected shows `tool`; expired muted line; pending card unchanged |
| `components/Messages/AssistantBubble.test.tsx` | `error_code="cancelled"` without result → neutral line (not rose box); cancelled with result → result + "Cancelled — partial result" |
| `lib/status.test.ts` (new) | tool branches read `p.tool` and render distinct text per type; model branches render without `#N`; default returns `Unknown event {type}`; real-payload fixtures (no `tool_name`/`call_count`) produce the new text, not the raw fallback |
| `components/ChatMessageList.test.tsx` | empty input renders placeholder; non-empty does not |

Acceptance: `npm test` (frontend) all green; manual smoke — submit a task,
watch Stop appear while running, click Stop, observe bubble + button return to
idle. (The approval-collapse smoke step is unreachable this round — pending
cards cannot render on real data, see §2/follow-ups §1; it ships with that
round's acceptance.)
