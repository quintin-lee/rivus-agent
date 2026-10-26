# API 参考 · 事件流（Events）

SSE 事件流是 Run 的可观测通道：所有执行进度（模型轮次、工具调用、审批、预算、校验）以追加事件落库，并经本端点实时推送 + 断点重放。

## GET /api/v1/runs/{id}/events

订阅指定 Run 的事件流（`text/event-stream`）。

| 断点续传参数 | 位置 | 说明 |
|---|---|---|
| `after` | 查询参数 `?after=<seq>` | 从该 seq 之后开始 |
| `Last-Event-ID` | 请求头 | 同上；标准 SSE 客户端自动携带 |

**响应** `200`，帧格式：

```text
id: <seq>
event: <event_type>
data: <payload_json>

```

心跳：每 15 秒一帧 `: ping`（注释帧，客户端忽略）。

**错误**：`404 not_found`（Run 不存在或跨 owner）。

```bash
# 从头订阅
curl -N localhost:8080/api/v1/runs/<run_id>/events

# 从 seq=42 之后续传（两种等价方式）
curl -N 'localhost:8080/api/v1/runs/<run_id>/events?after=42'
curl -N localhost:8080/api/v1/runs/<run_id>/events -H 'Last-Event-ID: 42'
```

## SSE 连接语义

- **重放**：先重放 `after` 之后的历史事件；若 Run 已终态，重放完即关闭连接
- **实时推送**：未终态则进入 follow 循环——内存 Hub 推送 + 每秒轮询兜底，事件不丢
- **单连接最长 5 分钟**：到期服务端主动关闭；标准 SSE 客户端会自动带着最后收到的 `Last-Event-ID` 重连
- **gap 回填**：推送缓冲满导致丢 seq 时，服务端自动从库补齐缺口
- **幂等去重**：回填与推送重叠时**可能重复投递同一 seq**（有意为之）；客户端按 `seq` 去重
- **非流式降级**：不支持 Flusher 的场景（部分代理）收到一次性完整重放后连接结束
- `sensitivity` 字段：事件元数据中的敏感级标记，当前全部为 `"normal"`（预留扩展）

## 事件类型目录

事件类型为稳定业务协议（`backend/internal/domain/event.go`）。`payload` 为 `data:` 行的 JSON 形状。

### 已发射（19）

| 事件 | 触发时机 | payload |
|---|---|---|
| `run.created` | Run 创建成功 | `{"goal":"created"}` |
| `run.started` | Worker 开始执行（含子任务） | `{"text":"run started","tool":""}` |
| `run.resumed` | Resume 路径开始执行 | `{"text":"run resumed","tool":""}` |
| `run.finished` | 校验通过，Run 成功 | `{"text":"<最终输出>","tool":""}` |
| `run.failed` | 执行失败（模型错误等） | `{"text":"<错误信息>","tool":""}` |
| `run.cancelled` | 客户端取消 / 断连取消 | `{"text":"<取消原因>","tool":""}` |
| `run.timed_out` | 预算时长耗尽 | `{"text":"run timed out"}` |
| `plan.created` | Plan-Execute 生成计划 | `{"text":"plan with N steps"}` |
| `model.token` | 流式输出增量（父 Run 合并全量） | `{"text":"<截至目前累计文本>","tool":""}` |
| `model.completed` | 一轮模型文本完成 | `{"text":"<本轮文本>","tool":""}` |
| `model.failed` | 模型调用失败 | `{"text":"<错误信息>","tool":""}` |
| `tool.completed` | 工具执行成功 | `{"text":"<工具名>","tool":"<工具名>"}` |
| `tool.failed` | 工具执行失败 | `{"text":"<错误信息>","tool":"<工具名>"}` |
| `budget.consumed` | 模型/工具步骤边界的累计用量 | `{"model_calls":int,"tool_calls":int,"duration_seconds":int}` |
| `budget.exceeded` | 预算超限，Run 被终止 | `{"text":"budget exceeded: <code>"}`（code 见 [`runs.md`](runs.md) 注意事项） |
| `approval.requested` | 高风险工具待审批 | 审批流：`{"text":"<工具名> requires approval","approval_id":"...","tool_name":"..."}`；Runner 中断路径：`{"text":"interrupted","tool":""}` |
| `approval.decided` | 审批已决定 | `{"approved":bool,"approval_id":"...","tool_name":"...","reason":"..."}` |
| `verification.passed` | 成功标准校验通过 | `{"text":"verification passed"}` |
| `verification.failed` | 成功标准校验未通过 | `{"text":"verification failed"}` |

### 已定义未发射（9，当前版本预留）

| 事件 | 说明 |
|---|---|
| `plan.updated` | 计划更新（预留） |
| `step.started` | Plan 步骤开始（预留） |
| `step.finished` | Plan 步骤结束（预留） |
| `model.requested` | 模型调用发起（预留） |
| `tool.requested` | 工具调用发起（预留） |
| `tool.authorized` | 工具授权通过（预留） |
| `tool.denied` | 工具被策略拒绝（预留） |
| `tool.started` | 工具开始执行（预留） |
| `checkpoint.saved` | 检查点落库（预留） |

## 客户端消费建议

- 按 `id:`（即 `seq`）单调去重；`seq` 也用作重连位点
- 按 `event:` 行分发处理；`data:` 为 JSON，形状见上表
- 终态事件（`run.finished` / `run.failed` / `run.cancelled` / `run.timed_out`）到达后可主动断开
- `model.token` 为流式增量合并后的**累计文本**，直接整体替换渲染即可
