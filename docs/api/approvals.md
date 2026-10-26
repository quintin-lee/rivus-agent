# API 参考 · 审批（Approvals）

高风险工具调用（写入类 / 外部副作用类）默认进入 `waiting_approval` 状态等待人工决定，不占用 Worker。全部端点受认证与 `X-Owner-ID` 归属约束（见 [`overview.md`](overview.md)）。

## GET /api/v1/runs/{id}/approvals

列出某 Run 的全部审批记录（用于页面刷新后恢复待审批卡片）。

**响应** `200`：`{"approvals": [Approval]}`，`Approval` 字段：

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | 审批 ID（路径参数 `approval_id` 即此值） |
| `run_id` | string | 所属 Run |
| `tool_call_id` | string | 工具调用 ID |
| `tool_name` | string | 工具名 |
| `args_hash` | string | 参数摘要哈希（审批绑定，防篡改） |
| `requested_by` | string | 发起人 |
| `approved_by` | string | 决定人（可省略） |
| `status` | string | `pending` / `approved` / `rejected` / `expired` |
| `reason` | string | 决定理由（可省略） |
| `expires_at` | int | 过期时间（Unix 毫秒；过期后不可再决定） |
| `created_at` | int | 创建时间（Unix 毫秒） |
| `decided_at` | int | 决定时间（可省略） |

**错误**：`404 not_found`（Run 不存在或跨 owner）。

```bash
curl localhost:8080/api/v1/runs/<run_id>/approvals
```

## POST /api/v1/runs/{id}/approvals/{approval_id}

决定一条审批。

| 请求字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `approve` | bool | 是 | 批准（true）/ 拒绝（false） |
| `reason` | string | 否 | 决定理由，记入审批记录与事件 |

**响应** `200`：`{"status":"decided"}`

**错误**：`409 conflict`——三种来源：审批已决定 / 已过期 / 无权决定（`"forbidden"`）。

```bash
curl -X POST localhost:8080/api/v1/runs/<run_id>/approvals/<approval_id> \
  -d '{"approve":true,"reason":"ok"}'
```

## 注意事项

- **批准**：Run 从 `waiting_approval` 恢复为 `running`，从 Checkpoint 续跑该工具调用
- **拒绝**：Run 以 `failed` 终态结束，`error_code` 为 `approval_rejected`
- 审批绑定 `args_hash` + `expires_at`：参数摘要防篡改，过期自动失效
- 决定动作不可撤销；重复决定返回 409
- 相关事件：`approval.requested`、`approval.decided`（见 [`events.md`](events.md)）
