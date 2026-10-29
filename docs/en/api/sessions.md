# API Reference · Sessions

Sessions are containers for Runs. All endpoints are subject to auth and `X-Owner-ID` ownership (see [`overview.md`](overview.md)).

## POST /api/v1/sessions

Create a session.

| Request field | Type | Required | Description |
|---|---|---|---|
| `title` | string | no | Session title |

**Response** `201`:

| Field | Type | Description |
|---|---|---|
| `session_id` | string | New session ID (`ses_` prefix) |

```bash
curl -X POST localhost:8080/api/v1/sessions -d '{"title":"demo"}'
# {"session_id":"ses_e74ac10f-..."}
```

## GET /api/v1/sessions

List sessions for the current owner.

**Query params**: `q` (optional) — case-insensitive substring match on the title; omit to list all.

**Response** `200`: `{"sessions": [SessionInfo]}` with `SessionInfo` fields:

| Field | Type | Description |
|---|---|---|
| `session_id` | string | Session ID |
| `title` | string | Title |
| `created_at` | int | Creation time (Unix ms) |
| `updated_at` | int | Update time (Unix ms) |

```bash
curl localhost:8080/api/v1/sessions
curl "localhost:8080/api/v1/sessions?q=refactor"
```

## GET /api/v1/sessions/{id}

Get a single session.

**Response** `200`: `{"session_id", "title", "created_at", "updated_at"}` (fields as above).

**Errors**: `404 not_found` (missing or owned by another owner).

```bash
curl localhost:8080/api/v1/sessions/<session_id>
```

## DELETE /api/v1/sessions/{id}

Delete a session.

**Response** `200`: `{"status":"deleted"}`

**Errors**:

| Status | Scenario |
|---|---|
| `409 conflict` | Session still has active runs — `"session has active runs; cancel them first"`; cancel them first |
| `404 not_found` | Missing or cross-owner |

```bash
curl -X DELETE localhost:8080/api/v1/sessions/<session_id>
```

## PATCH /api/v1/sessions/{id}

Rename a session.

**Request** `{"title": "new title"}` (trimmed non-empty; truncated beyond 200 chars)

**Response** `200`: `{"session_id":"...","title":"new title","created_at":...,"updated_at":...}`

**Errors**:

| Status | Scenario |
|---|---|
| `400 bad_request` | Empty title (renaming to nothing is meaningless) or invalid body |
| `404 not_found` | Missing or cross-owner |

```bash
curl -X PATCH localhost:8080/api/v1/sessions/<session_id> -d '{"title":"new title"}'
```

## GET /api/v1/sessions/{id}/memories

List session memories (persistent cross-run facts written by the `memory_write` tool; newest first). The latest 10 (≤8KB) are auto-injected into the model instruction of top-level runs in this session.

**Query params**: `limit` (1..200, default 200).

**Response** `200`: `{"memories":[{"id","session_id","owner_id","content","source_run_id","created_at"}]}`

**Error**: `404 not_found` (missing or owned by another owner).

```bash
curl localhost:8080/api/v1/sessions/<session_id>/memories
```

## Notes

- Deleting a session does not cascade-delete its Runs; cancel active runs before deleting
- After deletion, its Runs remain directly accessible via `GET /api/v1/runs/{id}`
- Deleting a session **does** cascade-delete its memories (`session_memories` FK cascade)
