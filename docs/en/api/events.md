# API Reference · Events

The SSE event stream is the observability channel for a Run: all execution progress (model rounds, tool calls, approvals, budget, verification) is appended to the store and pushed live via this endpoint, with replay support.

## GET /api/v1/runs/{id}/events

Subscribe to the event stream of a Run (`text/event-stream`).

| Replay parameter | Location | Description |
|---|---|---|
| `after` | Query param `?after=<seq>` | Start after this seq |
| `Last-Event-ID` | Header | Same; standard SSE clients send it automatically |

**Response** `200`, frame format:

```text
id: <seq>
event: <event_type>
data: <payload_json>

```

Heartbeat: a `: ping` comment frame every 15 seconds (ignored by clients).

**Errors**: `404 not_found` (run missing or cross-owner).

```bash
# Subscribe from the beginning
curl -N localhost:8080/api/v1/runs/<run_id>/events

# Resume after seq=42 (two equivalent ways)
curl -N 'localhost:8080/api/v1/runs/<run_id>/events?after=42'
curl -N localhost:8080/api/v1/runs/<run_id>/events -H 'Last-Event-ID: 42'
```

## SSE Connection Semantics

- **Replay**: historical events after `after` are replayed first; if the Run is already terminal, the connection closes after replay
- **Live push**: otherwise the server enters a follow loop — in-memory Hub push + a 1s polling backstop, so no events are lost
- **Max 5 minutes per connection**: the server then closes it; standard SSE clients automatically reconnect with the last received `Last-Event-ID`
- **Gap backfill**: when push buffering drops intermediate seqs, the server backfills from the store
- **Idempotent dedup**: backfill and push may overlap, so **duplicate delivery of the same seq is intentional**; clients dedupe by `seq`
- **Non-streaming fallback**: without a Flusher (behind some proxies), the client receives a one-shot full replay and the connection ends
- `sensitivity` field: a sensitivity marker in event metadata, currently always `"normal"` (reserved)

## Event Type Catalog

Event types are a stable business protocol (`backend/internal/domain/event.go`). `payload` is the JSON shape of the `data:` line.

### Emitted (19)

| Event | Trigger | payload |
|---|---|---|
| `run.created` | Run created | `{"goal":"created"}` |
| `run.started` | Worker starts executing (including subtasks) | `{"text":"run started","tool":""}` |
| `run.resumed` | Resume path starts executing | `{"text":"run resumed","tool":""}` |
| `run.finished` | Verification passed, Run succeeded | `{"text":"<final output>","tool":""}` |
| `run.failed` | Execution failed (model errors etc.) | `{"text":"<error message>","tool":""}` |
| `run.cancelled` | Client cancel / disconnect cancel | `{"text":"<cancel reason>","tool":""}` |
| `run.timed_out` | Duration budget exhausted | `{"text":"run timed out"}` |
| `plan.created` | Plan-Execute plan generated | `{"text":"plan with N steps"}` |
| `model.token` | Streaming output delta (coalesced full-so-far on parent runs) | `{"text":"<accumulated text>","tool":""}` |
| `model.completed` | One model round's text completed | `{"text":"<round text>","tool":""}` |
| `model.failed` | Model call failed | `{"text":"<error message>","tool":""}` |
| `tool.completed` | Tool execution succeeded | `{"text":"<tool name>","tool":"<tool name>"}` |
| `tool.failed` | Tool execution failed | `{"text":"<error message>","tool":"<tool name>"}` |
| `budget.consumed` | Cumulative usage at model/tool step boundaries | `{"model_calls":int,"tool_calls":int,"duration_seconds":int}` |
| `budget.exceeded` | Budget cap hit, Run terminated | `{"text":"budget exceeded: <code>"}` (codes listed in [`runs.md`](runs.md) notes) |
| `approval.requested` | High-risk tool awaiting approval | Approval flow: `{"text":"<tool name> requires approval","approval_id":"...","tool_name":"..."}`; Runner interrupt path: `{"text":"interrupted","tool":""}` |
| `approval.decided` | Approval decided | `{"approved":bool,"approval_id":"...","tool_name":"...","reason":"..."}` |
| `verification.passed` | Success criteria check passed | `{"text":"verification passed"}` |
| `verification.failed` | Success criteria check failed | `{"text":"verification failed"}` |

### Defined but not emitted (9, reserved in the current version)

| Event | Description |
|---|---|
| `plan.updated` | Plan updated (reserved) |
| `step.started` | Plan step started (reserved) |
| `step.finished` | Plan step finished (reserved) |
| `model.requested` | Model call initiated (reserved) |
| `tool.requested` | Tool call initiated (reserved) |
| `tool.authorized` | Tool authorized (reserved) |
| `tool.denied` | Tool denied by policy (reserved) |
| `tool.started` | Tool started (reserved) |
| `checkpoint.saved` | Checkpoint persisted (reserved) |

## Client Consumption Tips

- Dedupe monotonically by `id:` (i.e. `seq`); it also serves as the reconnect position
- Dispatch on the `event:` line; `data:` is JSON with shapes from the tables above
- After a terminal event (`run.finished` / `run.failed` / `run.cancelled` / `run.timed_out`) you may disconnect proactively
- `model.token` carries **coalesced full-so-far text**; render by wholesale replacement
