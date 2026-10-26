# API 参考 · 系统（System）

健康检查与指标端点。`/healthz`、`/readyz` **免认证**；`/metrics` 受认证约束（见 [`overview.md`](overview.md)）。

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

## 注意事项

- `/healthz` 与 `/readyz` 在认证中间件中显式放行，无需 Bearer Token
- `docker-compose.yml` 的 backend healthcheck 即基于 `/healthz`
