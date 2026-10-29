# API Reference · System

Health checks and metrics. `/healthz`, `/readyz`, and `/version` are **auth-exempt**; `/metrics` is subject to auth (see [`overview.md`](overview.md)).

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
| `rate_limited_total` | Requests rejected by rate limiting (429) |

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

## GET /metrics/prometheus

Exposes the same data as `/metrics` (expvar) in Prometheus text format (`text/plain; version=0.0.4`): counters are emitted directly and Map metrics are expanded to `name{key="..."}`. Auth required (same as `/metrics`).

```bash
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/metrics/prometheus
```

```text
# TYPE rate_limited_total counter
rate_limited_total 0
```

Suitable for direct scraping by a Prometheus server; built-in expvar metrics (`cmdline`, `memstats`) are not emitted.

## GET /version

Build version info (auth-exempt). The triple is injected via `-ldflags -X`; without injection it reads `dev` / `unknown` / `unknown`.

```bash
curl localhost:8080/version
```

```json
{"version":"v1.2.3","commit":"47839d7","build_date":"2026-10-27T03:00:00Z"}
```

## Notes

- `/healthz` and `/readyz` are explicitly allowed through the auth middleware, no Bearer token needed
- `/version` is also auth-exempt and returns the injected build triple; inject locally with `make build VERSION=v1.2.3`
- The backend healthcheck in `docker-compose.yml` is based on `/healthz`
- `/debug/pprof/*` (index / cmdline / profile / symbol / trace) is registered only when `AGENT_AUTH_TOKEN` is configured; deployments without auth return 404 for these paths. Named profiles (heap, goroutine, etc.) are covered by the index handler, e.g. `GET /debug/pprof/heap?debug=1`
