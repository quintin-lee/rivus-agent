# API Reference · Approvals

High-risk tool calls (write / external side-effect classes) enter `waiting_approval` by default and wait for a human decision without occupying a Worker. All endpoints are subject to auth and `X-Owner-ID` ownership (see [`overview.md`](overview.md)).

## GET /api/v1/runs/{id}/approvals

List all approval records of a Run (for restoring pending approval cards after a page refresh).

**Response** `200`: `{"approvals": [Approval]}` with `Approval` fields:

| Field | Type | Description |
|---|---|---|
| `id` | string | Approval ID (the `approval_id` path parameter) |
| `run_id` | string | Owning Run |
| `tool_call_id` | string | Tool call ID |
| `tool_name` | string | Tool name |
| `args_hash` | string | Argument digest hash (approval binding, tamper-proof) |
| `requested_by` | string | Requester |
| `approved_by` | string | Decider (omitempty) |
| `status` | string | `pending` / `approved` / `rejected` / `expired` |
| `reason` | string | Decision reason (omitempty) |
| `expires_at` | int | Expiry time (Unix ms; decisions after expiry are rejected) |
| `created_at` | int | Creation time (Unix ms) |
| `decided_at` | int | Decision time (omitempty) |

**Errors**: `404 not_found` (run missing or cross-owner).

```bash
curl localhost:8080/api/v1/runs/<run_id>/approvals
```

## POST /api/v1/runs/{id}/approvals/{approval_id}

Decide an approval.

| Request field | Type | Required | Description |
|---|---|---|---|
| `approve` | bool | yes | Approve (true) / reject (false) |
| `reason` | string | no | Decision reason, recorded in the approval row and events |

**Response** `200`: `{"status":"decided"}`

**Errors**: `409 conflict` — three sources: already decided / expired / not permitted (`"forbidden"`).

```bash
curl -X POST localhost:8080/api/v1/runs/<run_id>/approvals/<approval_id> \
  -d '{"approve":true,"reason":"ok"}'
```

## Notes

- **Approve**: the Run leaves `waiting_approval` and returns to `running`, resuming that tool call from the checkpoint
- **Reject**: the Run ends as `failed` with `error_code` `approval_rejected`
- Approvals bind `args_hash` + `expires_at`: an argument digest prevents tampering, and expiry invalidates automatically
- Decisions are irrevocable; deciding again returns 409
- Related events: `approval.requested`, `approval.decided` (see [`events.md`](events.md))
