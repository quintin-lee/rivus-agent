# API 参考 · 运行（Runs）

Run 是一次任务执行。模式 `react`（ReAct 工具循环，缺省）或 `plan_execute`（Plan-Execute）。全部端点受认证与 `X-Owner-ID` 归属约束（见 [`overview.md`](overview.md)）。

## GET /api/v1/runs

列出某会话下的 Run。

| 查询参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `session_id` | string | 是 | 会话 ID，缺失返回 400 |
| `limit` | int | 否 | 返回条数，缺省 50 |

**响应** `200`：`{"runs": [Run]}`，`Run` 字段：

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | Run ID（`run_` 前缀） |
| `session_id` | string | 所属会话 |
| `parent_run_id` | string | 子任务父 Run（可省略） |
| `owner_id` | string | 归属 |
| `status` | string | `queued` / `running` / `waiting_approval` / `paused` / `succeeded` / `failed` / `cancelled` / `timed_out` |
| `mode` | string | `react` / `plan_execute` |
| `goal` | string | 任务目标 |
| `constraints` | []string | 约束（可省略） |
| `success_criteria` | []string | 成功标准（可省略） |
| `budget` | object | 收紧后的实际预算，5 个 `max_*` 字段（见 [`overview.md`](overview.md)） |
| `checkpoint_id` | string | 中断恢复检查点（可省略） |
| `attempt` | int | 执行轮次，从 1 起 |
| `idempotency_key` | string | 幂等键（可省略） |
| `error_code` | string | 失败错误码（可省略，如 `verification_failed`） |
| `error_summary` | string | 失败摘要（可省略） |
| `result_json` | string | 校验报告 JSON（终态，可省略） |
| `created_at` / `updated_at` | int | Unix 毫秒 |
| `started_at` / `finished_at` | int | Unix 毫秒（可省略） |

**错误**：`400 bad_request`（缺 `session_id`）/ `404 not_found`（会话不存在或跨 owner）。

```bash
curl 'localhost:8080/api/v1/runs?session_id=<session_id>&limit=20'
```

## POST /api/v1/runs

提交任务。入队后由有界 Worker 异步执行；事件经 SSE 流式返回。

| 请求头 | 说明 |
|---|---|
| `Idempotency-Key` | 可选；同 key 重复提交返回同一 Run 且 `duplicated: true` |

| 请求字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `session_id` | string | 是 | 所属会话 |
| `goal` | string | 是 | 任务目标 |
| `constraints` | []string | 否 | 约束 |
| `success_criteria` | []string | 否 | 成功标准（用于完成时校验） |
| `mode` | string | 否 | `react`（缺省）/ `plan_execute` |
| `budget` | object | 否 | 预算申请，服务端只紧不松（5 个 `max_*` 字段） |

**响应** `201`：

| 字段 | 类型 | 说明 |
|---|---|---|
| `run_id` | string | 新 Run ID |
| `status` | string | 初始为 `queued` |
| `duplicated` | bool | 是否幂等命中 |

**错误**：`400 bad_request`（`"invalid goal/session"`）。

```bash
curl -X POST localhost:8080/api/v1/runs \
  -H 'Idempotency-Key: demo-1' \
  -d '{
    "session_id": "<session_id>",
    "goal": "分析项目并给出重构建议",
    "mode": "react",
    "success_criteria": ["输出风险列表", "每条建议有证据"],
    "budget": {
      "max_duration_seconds": 600,
      "max_model_calls": 30,
      "max_tool_calls": 50,
      "max_iterations": 12
    }
  }'
```

> 提交 Run 本身无需模型可用；但 Run 执行需要模型三件套（`MODEL_BASE_URL` / `MODEL_API_KEY` / `MODEL_NAME`）配置正确，否则 Run 以 `model_failed` 类错误终态。

## GET /api/v1/runs/{id}

查看 Run 详情与其全部步骤（Plan-Execute）。

**响应** `200`：`{"run": Run, "steps": [Step]}`，`Step` 字段：

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | 步骤 ID |
| `run_id` | string | 所属 Run |
| `step_index` | int | 序号（0 起） |
| `description` | string | 步骤描述 |
| `status` | string | `pending` / `running` / `succeeded` / `failed` / `skipped` / `awaiting_approval` |
| `dependencies` | []int | 依赖的 step_index（可省略） |
| `result_summary` | string | 执行结果摘要（可省略） |
| `started_at` / `finished_at` | int | Unix 毫秒（可省略） |

**错误**：`404 not_found`。

```bash
curl localhost:8080/api/v1/runs/<run_id>
```

## POST /api/v1/runs/{id}/cancel

取消 Run。

**响应** `200`：`{"status":"cancelled"}`

**错误**：`409 conflict` `"run is not in a cancellable state"`（终态不可取消；`waiting_approval` 可取消）。

```bash
curl -X POST localhost:8080/api/v1/runs/<run_id>/cancel
```

## POST /api/v1/runs/{id}/resume

恢复暂停 / 中断的任务（从 SQLite Checkpoint 续跑）。

**响应** `200`：`{"status":"running"}`

**错误**：`409 conflict` `"run is not in a resumable state"`。

```bash
curl -X POST localhost:8080/api/v1/runs/<run_id>/resume
```

## 注意事项

- 终态（`succeeded` / `failed` / `cancelled` / `timed_out`）不可再取消或恢复
- `waiting_approval` 状态的 Run 不占 Worker；批准走 [`approvals.md`](approvals.md)，拒绝后 Run 以 `approval_rejected` 失败
- 预算超限的 Run 以 `failed` 终态 + `error_code` 为 `tool_calls_budget_exceeded` / `model_calls_budget_exceeded` / `output_bytes_budget_exceeded` 之一，并发 `budget.exceeded` 事件（见 [`events.md`](events.md)）
