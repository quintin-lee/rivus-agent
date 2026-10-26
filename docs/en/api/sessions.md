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

**Response** `200`: `{"sessions": [SessionInfo]}` with `SessionInfo` fields:

| Field | Type | Description |
|---|---|---|
| `session_id` | string | Session ID |
| `title` | string | Title |
| `created_at` | int | Creation time (Unix ms) |
| `updated_at` | int | Update time (Unix ms) |

```bash
curl localhost:8080/api/v1/sessions
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

## Notes

- Deleting a session does not cascade-delete its Runs; cancel active runs before deleting
- After deletion, its Runs remain directly accessible via `GET /api/v1/runs/{id}`
