# API Reference · Overview

REST / SSE API reference for the Rivus Agent backend. Base URL: `http://localhost:8080` (change via `AGENT_ADDR`).

> This page covers general conventions; per-endpoint details live in the navigation. All endpoints are documented in [`sessions.md`](sessions.md), [`runs.md`](runs.md), [`events.md`](events.md), [`approvals.md`](approvals.md), [`settings.md`](settings.md), and [`system.md`](system.md).

## Basic Conventions

- Requests and responses are `application/json` (except the SSE endpoint, which returns `text/event-stream`)
- Request body limit is **1 MiB**; oversized or invalid JSON returns `400`
- Timestamp fields (`created_at` / `updated_at` / `expires_at`, etc.) are **Unix milliseconds integers**
- ID prefixes: sessions `ses_`, runs `run_`

## Pagination

List endpoints accept `limit` / `offset` query parameters (`GET /api/v1/sessions`, `GET /api/v1/runs`):

| Endpoint | limit default | limit cap | offset default |
|---|---|---|---|
| `GET /api/v1/sessions` | 50 | 200 | 0 |
| `GET /api/v1/runs` | 50 | 100 | 0 |

Results are always **newest first**; an out-of-range `offset` returns an empty list (`200` + empty array, not an error). Both endpoints also support **cursor pagination**: the response carries `next_cursor` (base64url) to pass back as the `cursor` parameter for the next page, avoiding OFFSET scans on deep pages; a non-empty `cursor` overrides `offset`. The frontend uses a "Load more" button that pages via cursor.

## Search

List endpoints accept a `q` parameter for case-insensitive substring filtering:

| Endpoint | Matches |
|---|---|
| `GET /api/v1/sessions?q=` | Session title |
| `GET /api/v1/runs?q=` | Run goal and result (`result_json`) |

For runs, a `q` term of ≥3 bytes uses SQLite FTS5 full-text phrase matching (trigram tokenizer, double quotes escaped automatically); shorter terms fall back to LIKE substring matching. Both paths return the same row set. MCP servers can be configured with OAuth client-credentials (see [settings.md](settings.md)); `client_secret` is write-only and never echoed.

## Authentication

When the `AGENT_AUTH_TOKEN` env var is set, all routes except `/healthz`, `/readyz`, and `/version` require a Bearer token:

```bash
curl -H "Authorization: Bearer <token>" localhost:8080/api/v1/sessions
```

An empty `AGENT_AUTH_TOKEN` means **no auth** (local development only). Missing or wrong token:

```json
401 {"code":"unauthorized","message":"missing or invalid token","retryable":false}
```

## Multi-Tenant Ownership

All business routes isolate data by the `X-Owner-ID` request header, defaulting to `"default"`. Cross-owner access always returns `404` (existence is not leaked):

```bash
curl -H "X-Owner-ID: alice" localhost:8080/api/v1/runs/<run_id>
```

## Request ID (X-Request-ID)

Clients may send an `X-Request-ID` header (≤64 chars, charset `[0-9a-zA-Z-._]`, other characters stripped); when absent a 32-hex id is generated. Every response echoes the header and the structured access log carries `request_id` for troubleshooting.

## Error Format

All error responses share one shape:

```json
{"code": "<string>", "message": "<string>", "retryable": <bool>}
```

Common `code` values:

| code | Typical status | Meaning |
|---|---|---|
| `bad_request` | 400 | Invalid body / missing required parameter |
| `not_found` | 404 | Resource not found (includes cross-owner access) |
| `conflict` | 409 | State machine rejects the operation (e.g. cancelling a terminal run) |
| `unauthorized` | 401 | Bearer token missing or wrong |
| `rate_limited` | 429 | Rate limit triggered (`retryable: true`; see the `Retry-After` response header) |
| `internal` | 500 | Server-side internal error (`retryable: true`) |
| `tools_not_configured` | 501 | No Tools Provider configured (settings/tools) |
| `not_ready` | 503 | Database not ready (readyz, `retryable: true`) |

## Idempotency Key (Idempotency-Key)

Only `POST /api/v1/runs` supports it. Send an `Idempotency-Key` header; resubmitting the same key returns **the same Run** with `duplicated: true` in the response (`false` on first submission). Behavior is undefined for the same key with a different body — keep key↔content mapping stable.

## Budgets: Tightened, Never Relaxed

Runs may request a budget, but the server **tightens** it against its own caps (`Budget.ClampByServer`): values `<= 0` or above a cap become the cap; in-range values pass through unchanged.

| Budget dimension | Server default cap |
|---|---|
| `max_duration_seconds` | 600 |
| `max_model_calls` | 30 |
| `max_tool_calls` | 50 |
| `max_iterations` | 12 |
| `max_output_bytes` | 200000 |

Caps are tunable via YAML config (`backend/configs/config.example.yaml`); responses return the **effective (tightened)** budget in `run.budget`.

## Document Map

| Document | Contents |
|---|---|
| [`sessions.md`](sessions.md) | Session management (6 endpoints, incl. memories and rename) |
| [`runs.md`](runs.md) | Run lifecycle (5 endpoints) |
| [`events.md`](events.md) | SSE event stream + full event type catalog |
| [`approvals.md`](approvals.md) | Human approval (2 endpoints) |
| [`settings.md`](settings.md) | Skills/MCP and model settings (4 endpoints) |
| [`system.md`](system.md) | Health / readiness / metrics / version (4 endpoints) |

Chinese version: [`docs/api/`](../../api/overview.md).
