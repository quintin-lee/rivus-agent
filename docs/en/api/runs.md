# API Reference · Runs

A Run is one task execution. Modes: `react` (ReAct tool loop, default) or `plan_execute` (Plan-Execute). All endpoints are subject to auth and `X-Owner-ID` ownership (see [`overview.md`](overview.md)).

## GET /api/v1/runs

List runs: with `session_id`, lists runs of that session; omitting `session_id` returns the owner's cross-session **top-level** run history (`parent_run_id IS NULL`; sub-agent runs excluded).

| Query param | Type | Required | Description |
|---|---|---|---|
| `session_id` | string | no | Session ID; when provided lists that session only (incl. child runs), when missing returns cross-session top-level history |
| `status` | string | no | Filter by run status (one of queued/running/waiting_approval/paused/succeeded/failed/cancelled/timed_out); invalid values return 400 |
| `limit` | int | no | Page size, default 50 |
| `offset` | int | no | Pagination offset (skips the N newest, combine with `limit`), default 0 |

**Response** `200`: `{"runs": [Run]}` with `Run` fields:

| Field | Type | Description |
|---|---|---|
| `id` | string | Run ID (`run_` prefix) |
| `session_id` | string | Owning session |
| `parent_run_id` | string | Parent run of a subtask (omitempty) |
| `owner_id` | string | Owner |
| `status` | string | `queued` / `running` / `waiting_approval` / `paused` / `succeeded` / `failed` / `cancelled` / `timed_out` |
| `mode` | string | `react` / `plan_execute` |
| `goal` | string | Task goal |
| `constraints` | []string | Constraints (omitempty) |
| `success_criteria` | []string | Success criteria (omitempty) |
| `budget` | object | Effective (tightened) budget, five `max_*` fields (see [`overview.md`](overview.md)) |
| `checkpoint_id` | string | Interrupt-recovery checkpoint (omitempty) |
| `attempt` | int | Execution attempt, starting at 1 |
| `idempotency_key` | string | Idempotency key (omitempty) |
| `error_code` | string | Failure code (omitempty, e.g. `verification_failed`) |
| `error_summary` | string | Failure summary (omitempty) |
| `result_json` | string | Verification report JSON (terminal, omitempty) |
| `created_at` / `updated_at` | int | Unix ms |
| `started_at` / `finished_at` | int | Unix ms (omitempty) |

**Errors**: `400 bad_request` (missing `session_id`) / `404 not_found` (session missing or cross-owner).

```bash
curl 'localhost:8080/api/v1/runs?session_id=<session_id>&limit=20'
```

## POST /api/v1/runs

Submit a task. It is queued and executed asynchronously by a bounded Worker; progress streams via SSE.

| Header | Description |
|---|---|
| `Idempotency-Key` | Optional; resubmitting the same key returns the same Run with `duplicated: true` |

| Request field | Type | Required | Description |
|---|---|---|---|
| `session_id` | string | yes | Owning session |
| `goal` | string | yes | Task goal |
| `constraints` | []string | no | Constraints |
| `success_criteria` | []string | no | Success criteria (checked at completion) |
| `mode` | string | no | `react` (default) / `plan_execute` |
| `budget` | object | no | Budget request, tightened by the server (five `max_*` fields) |

**Response** `201`:

| Field | Type | Description |
|---|---|---|
| `run_id` | string | New Run ID |
| `status` | string | Initially `queued` |
| `duplicated` | bool | Whether this was an idempotent hit |

**Errors**: `400 bad_request` (`"invalid goal/session"`).

```bash
curl -X POST localhost:8080/api/v1/runs \
  -H 'Idempotency-Key: demo-1' \
  -d '{
    "session_id": "<session_id>",
    "goal": "Analyze the project and propose refactoring suggestions",
    "mode": "react",
    "success_criteria": ["Produce a risk list", "Every suggestion backed by evidence"],
    "budget": {
      "max_duration_seconds": 600,
      "max_model_calls": 30,
      "max_tool_calls": 50,
      "max_iterations": 12
    }
  }'
```

> Submitting a Run does not require a working model; but execution does — configure the model trio (`MODEL_BASE_URL` / `MODEL_API_KEY` / `MODEL_NAME`) correctly, otherwise the Run terminates with a `model_failed` error.

## GET /api/v1/runs/{id}

Run details plus all steps (Plan-Execute).

**Response** `200`: `{"run": Run, "steps": [Step]}` with `Step` fields:

| Field | Type | Description |
|---|---|---|
| `id` | string | Step ID |
| `run_id` | string | Owning Run |
| `step_index` | int | Zero-based index |
| `description` | string | Step description |
| `status` | string | `pending` / `running` / `succeeded` / `failed` / `skipped` / `awaiting_approval` |
| `dependencies` | []int | Dependent step_index values (omitempty) |
| `result_summary` | string | Execution result summary (omitempty) |
| `started_at` / `finished_at` | int | Unix ms (omitempty) |

**Errors**: `404 not_found`.

```bash
curl localhost:8080/api/v1/runs/<run_id>
```

## POST /api/v1/runs/{id}/cancel

Cancel a Run.

**Response** `200`: `{"status":"cancelled"}`

**Errors**: `409 conflict` `"run is not in a cancellable state"` (terminal runs cannot be cancelled; `waiting_approval` can).

```bash
curl -X POST localhost:8080/api/v1/runs/<run_id>/cancel
```

## POST /api/v1/runs/{id}/resume

Resume a paused / interrupted task (continues from the SQLite checkpoint).

**Response** `200`: `{"status":"running"}`

**Errors**: `409 conflict` `"run is not in a resumable state"`.

```bash
curl -X POST localhost:8080/api/v1/runs/<run_id>/resume
```

## Notes

- Terminal states (`succeeded` / `failed` / `cancelled` / `timed_out`) can no longer be cancelled or resumed
- Runs in `waiting_approval` occupy no Worker; approve via [`approvals.md`](approvals.md) — rejecting fails the Run with `approval_rejected`
- Budget-exhausted Runs end as `failed` with `error_code` one of `tool_calls_budget_exceeded` / `model_calls_budget_exceeded` / `output_bytes_budget_exceeded`, plus a `budget.exceeded` event (see [`events.md`](events.md))
