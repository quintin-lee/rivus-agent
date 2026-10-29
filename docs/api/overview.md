# API 参考 · 概览

Rivus Agent 后端 REST / SSE API 参考。Base URL：`http://localhost:8080`（`AGENT_ADDR` 可改）。

> 本文描述通用约定；各端点细节见目录导航。全部端点见 [`sessions.md`](sessions.md)、[`runs.md`](runs.md)、[`events.md`](events.md)、[`approvals.md`](approvals.md)、[`settings.md`](settings.md)、[`system.md`](system.md)。

## 基础约定

- 请求与响应均为 `application/json`（SSE 端点除外，返回 `text/event-stream`）
- 请求体上限 **1 MiB**，超限或非法 JSON 返回 `400`
- 时间戳字段（`created_at` / `updated_at` / `expires_at` 等）均为 **Unix 毫秒整数**
- ID 前缀：会话 `ses_`、运行 `run_`

## 分页

列表端点支持 `limit` / `offset` 查询参数（`GET /api/v1/sessions`、`GET /api/v1/runs`）：

| 端点 | limit 缺省 | limit 上限 | offset 缺省 |
|---|---|---|---|
| `GET /api/v1/sessions` | 50 | 200 | 0 |
| `GET /api/v1/runs` | 50 | 100 | 0 |

结果一律**最新在前**；`offset` 越界返回空列表（`200` + 空数组，非错误）。前端以「加载更多」按当前页大小递增 offset。

## 认证

设置环境变量 `AGENT_AUTH_TOKEN` 后，除 `/healthz`、`/readyz`、`/version` 外的所有路由都要求 Bearer Token：

```bash
curl -H "Authorization: Bearer <token>" localhost:8080/api/v1/sessions
```

`AGENT_AUTH_TOKEN` 为空时**不鉴权**（仅限本地开发）。Token 缺失或错误：

```json
401 {"code":"unauthorized","message":"missing or invalid token","retryable":false}
```

## 多租户归属

所有业务路由按请求头 `X-Owner-ID` 隔离数据，缺省 `"default"`。跨 owner 访问一律返回 `404`（不泄露资源是否存在）：

```bash
curl -H "X-Owner-ID: alice" localhost:8080/api/v1/runs/<run_id>
```

## 请求标识（X-Request-ID）

客户端可携带 `X-Request-ID` 请求头（≤64 字符，字符集 `[0-9a-zA-Z-._]`，其余字符剥除）；缺省自动生成 32 位 hex。所有响应回显该头，结构化访问日志按 `request_id` 关联，便于排障串联。

## 错误格式

所有错误响应统一为：

```json
{"code": "<string>", "message": "<string>", "retryable": <bool>}
```

常见 `code`：

| code | 典型状态码 | 含义 |
|---|---|---|
| `bad_request` | 400 | 请求体非法 / 缺少必填参数 |
| `not_found` | 404 | 资源不存在（含跨 owner 访问） |
| `conflict` | 409 | 状态机不允许该操作（如终态 Run 再取消） |
| `unauthorized` | 401 | Bearer Token 缺失或错误 |
| `rate_limited` | 429 | 触发速率限制（`retryable: true`，等待秒数见 `Retry-After` 响应头） |
| `internal` | 500 | 服务端内部错误（`retryable: true`） |
| `tools_not_configured` | 501 | 未配置 Tools Provider（settings/tools） |
| `not_ready` | 503 | 数据库未就绪（readyz，`retryable: true`） |

## 幂等键（Idempotency-Key）

仅 `POST /api/v1/runs` 支持。请求头携带 `Idempotency-Key`，同 key 重复提交返回**同一个 Run**，响应中 `duplicated: true`（首次提交为 `false`）。相同 key + 不同请求体的行为未定义，请保证同 key 同内容。

## 预算只紧不松

创建 Run 时可申请预算，但服务端会按自身上限**收紧**（`Budget.ClampByServer`）：申请值 `<= 0` 或超过上限时取上限；上限内原样通过。

| 预算维度 | 服务端缺省上限 |
|---|---|
| `max_duration_seconds` | 600 |
| `max_model_calls` | 30 |
| `max_tool_calls` | 50 |
| `max_iterations` | 12 |
| `max_output_bytes` | 200000 |

上限经 YAML 配置（`backend/configs/config.example.yaml`）调整；响应中返回的是**收紧后**的实际预算（`run.budget`）。

## 文档目录

| 文档 | 内容 |
|---|---|
| [`sessions.md`](sessions.md) | 会话管理（6 端点，含会话记忆与重命名） |
| [`runs.md`](runs.md) | 运行生命周期（5 端点） |
| [`events.md`](events.md) | SSE 事件流 + 全量事件类型目录 |
| [`approvals.md`](approvals.md) | 人工审批（2 端点） |
| [`settings.md`](settings.md) | Skills/MCP 与模型设置（4 端点） |
| [`system.md`](system.md) | 健康/就绪/指标/版本（4 端点） |

英文版：[`docs/en/api/`](../en/api/overview.md)。
