# API Reference · System

Health checks and metrics. `/healthz` and `/readyz` are **auth-exempt**; `/metrics` is subject to auth (see [`overview.md`](overview.md)).

## GET /healthz

Liveness check.

**Response** `200`: `{"status":"ok"}`

```bash
curl localhost:8080/healthz
```

## GET /readyz

Readiness check (database connectivity).

**Response** `200`: `{"status":"ready"}`

**Errors**: `503 not_ready` (`retryable: true`) — database unreachable.

```bash
curl localhost:8080/readyz
```

## GET /metrics

Raw expvar metrics (JSON key/value pairs, **not** wrapped in the unified error format). Container healthchecks commonly use `wget /healthz`.

Common metrics:

| Metric | Description |
|---|---|
| `runs_started` | Total runs started |
| `runs_succeeded` / `runs_failed` | Terminal run counts |
| `tool_calls_total` | Call counts per tool name (Map) |
| `tool_errors_total` | Failure counts per tool name (Map) |
| `latency_ms_0_50` … `latency_ms_500_plus` | Tool execution latency histogram (5 buckets: `<50ms` / `50-100` / `100-200` / `200-500` / `≥500`) |
| `event_append_failed` | Event persistence failure count |

**Response** `200`, example:

```text
{
  "runs_started": 42,
  "latency_ms_0_50": 17,
  ...
}
```

```bash
curl localhost:8080/metrics
```

## Notes

- `/healthz` and `/readyz` are explicitly allowed through the auth middleware, no Bearer token needed
- The backend healthcheck in `docker-compose.yml` is based on `/healthz`
