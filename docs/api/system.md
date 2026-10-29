# API 参考 · 系统（System）

健康检查与指标端点。`/healthz`、`/readyz`、`/version` **免认证**；`/metrics` 受认证约束（见 [`overview.md`](overview.md)）。

## GET /healthz

存活检查。

**响应** `200`：`{"status":"ok"}`

```bash
curl localhost:8080/healthz
```

## GET /readyz

就绪检查（数据库连通性）。

**响应** `200`：`{"status":"ready"}`

**错误**：`503 not_ready`（`retryable: true`）——数据库不可达。

```bash
curl localhost:8080/readyz
```

## GET /metrics

expvar 原始指标（JSON 键值对，**非统一错误包裹格式**）。容器编排健康检查常用 `wget /healthz`。

常用指标：

| 指标 | 说明 |
|---|---|
| `runs_started` | 已启动 Run 总数 |
| `runs_succeeded` / `runs_failed` | 终态 Run 计数 |
| `tool_calls_total` | 按工具名的调用计数（Map） |
| `tool_errors_total` | 按工具名的失败计数（Map） |
| `latency_ms_0_50` … `latency_ms_500_plus` | 工具执行延迟直方图（5 桶：`<50ms` / `50-100` / `100-200` / `200-500` / `≥500`） |
| `event_append_failed` | 事件落库失败计数 |
| `rate_limited_total` | 触发速率限制（429）的请求数 |

**响应** `200`，示例：

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

以 Prometheus 文本格式（`text/plain; version=0.0.4`）暴露与 `/metrics`（expvar）相同的数据：计数器直接输出，Map 指标展开为 `name{key="..."}`。需要认证（与 `/metrics` 一致）。

```bash
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/metrics/prometheus
```

```text
# TYPE rate_limited_total counter
rate_limited_total 0
```

适合 Prometheus server 直接 scrape；expvar 内建指标（`cmdline`、`memstats`）不输出。

## GET /version

构建版本信息（免认证）。三元组经 `-ldflags -X` 注入，未注入时为 `dev` / `unknown` / `unknown`。

```bash
curl localhost:8080/version
```

```json
{"version":"v1.2.3","commit":"47839d7","build_date":"2026-10-27T03:00:00Z"}
```

## 注意事项

- `/healthz` 与 `/readyz` 在认证中间件中显式放行，无需 Bearer Token
- `/version` 同样免认证，返回构建注入的版本三元组；本地 `make build VERSION=v1.2.3` 可注入
- `docker-compose.yml` 的 backend healthcheck 即基于 `/healthz`
- `/debug/pprof/*`（索引 / cmdline / profile / symbol / trace）仅在配置了 `AGENT_AUTH_TOKEN` 时注册；未配置认证的部署访问这些路径返回 404。命名 profile（heap、goroutine 等）由索引 handler 覆盖，如 `GET /debug/pprof/heap?debug=1`
