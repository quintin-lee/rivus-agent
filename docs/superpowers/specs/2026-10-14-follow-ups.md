# Frontend UX Polish — Follow-ups (backend-dependent)

Deferred from `2026-10-14-frontend-ux-polish-design.md` (frontend-only round).
Each item requires a backend change; the frontend rendering layer ships this
round so it can be wired up without a second design pass.

## 1. Approval pairing + decision API (unblocks §2 pending card)

Current gap: `approval.requested` event payloads carry
`{"text":"{tool} requires approval"}` — no `approval_id`, no `tool_name` key.
`approval.decided` carries `{"approved":true|false}` only. The domain store
has full `domain.Approval` records and `POST /runs/{id}/approvals/{approval_id}`
works, but the frontend has no way to learn `approval_id`/`tool_name` for a
pending approval, so `deriveApprovals` returns `[]` and the pending card +
`api.decideApproval` are dead UI.

Backend work:
- Add `GET /api/v1/runs/{id}/approvals` returning pending approvals
  (`approval_id`, `tool_name`, `args_hash`, `reason`, `expires_at`,
  `requested_by`).
- Enrich `approval.requested` / `approval.decided` event payloads to embed
  `approval_id` + `tool_name` (map `tool_call_id` → `approval_id` via the
  approval store at emit time in `runtime.go` / `run_service.go`).

Then the frontend reuses the `p.approval_id`/`p.tool_name` keys that
`parseApprovalPayload` already reads — no frontend rework needed.

## 2. `ErrTimeout` → `RunTimedOut` mapping (unblocks §5 timed-out row)

`budget.go:38` returns `domain.ErrTimeout` when the duration budget is hit, but
no code path maps it to the `RunTimedOut` status — `runner_eino.go` /
`runtime.go` never call `UpdateStatus(_, RunTimedOut, …)`. The `timed_out`
status is reachable in the state machine but never produced.

Backend work: in the run lifecycle (`runtime.go`), when the context deadline
expires (or `ErrTimeout` is returned), transition to
`domain.RunTimedOut` with a clear `error_code` instead of `RunFailed`.

## 3. Live progress / budget events

Not in the current event vocabulary. Add e.g. `budget.consumed`
(`{model_calls, tool_calls, duration_seconds}`) emitted at step boundaries so
the running status bubble can show live progress.

## 4. Streaming token deltas

Add an SSE delta event (e.g. `model.token`) so results can stream live instead
of appearing only at run completion.

## 5. Stuck pending-goal dismiss

Manual dismiss of `pendingGoals` in the `failed` state with no re-queue.
