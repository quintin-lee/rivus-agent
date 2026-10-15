# 审批事件 payload 打通 — 设计文档

- **日期**: 2026-10-15
- **范围**: 后端 `backend/internal/runtime/runtime.go` + `backend/internal/service/run_service.go`。前端**零改动**。
- **状态**: 设计已确认（3 段）。
- **方案**: A1 — 定点补 payload，最小改动闭环。
- **上级 spec**: `docs/superpowers/specs/2026-10-14-frontend-ux-polish-design.md` §2（审批渲染层已就绪）；本 spec 是其 follow-ups §1（approval pairing）的后端半，**仅实现"补事件 payload"这一档（scope A）**，不含 `GET /runs/{id}/approvals` 读端点（scope B）。

## 背景 / 问题

上一轮前端 polish 落地了审批卡的渲染层（`deriveApprovals` 的 `expired` 派生、`ApprovalCard` 已决策单行、`parseApprovalPayload` 按 `approval_id`/`tool_name`/`approved`/`reason` 读取）。但**后端事件 payload 不带这些字段**：

| 事件 | 现状 payload | 缺口 |
|---|---|---|
| `approval.requested`（`runtime.go:100`） | `{"text":"{tool} requires approval"}` | 无 `approval_id`、无 `tool_name` |
| `approval.decided`（`run_service.go:158`） | `{"approved":true}` | 无 `approval_id`、无 `tool_name`、无 `reason` |

结果：前端 `parseApprovalPayload` 读 `approval_id` 落空 → `deriveApprovals` 的 `requested` Map 为空 → 审批卡渲染不出、`api.decideApproval(runId, approvalId)` 无 `approvalId` 可调 → **pending 审批是死 UI**。

**关键事实（已核实）**：`Approvals.Create`（`runtime.go:94`）在事件发射前已生成 `a.ID`（`newID("apr")`，见 `approval_repo.go:16`），所以权威路径拿 `approval_id` 是现成的，零 schema/DB 变更。

## 审批事件的两条发射路径

| 路径 | 位置 | 是否建审批记录 | 本 spec 处理 |
|---|---|---|---|
| **权威路径** | `runtime.go:87-100`（`onNeedApproval` handler，调 `Approvals.Create`） | ✅ 建记录 | **补 payload**（带 `a.ID`） |
| **副信号路径** | `runner_eino.go:277`（drain 收 `ErrNeedsApproval`）、`:302`（agent 中断） | ❌ 不建记录 | **不动**（无 `approval_id` 可带） |

副信号路径保持 `{text}` 不变。前端 `parseApprovalPayload` 的 `if (p.approvalId)` 守卫天然把它们滤掉（无 id 不建 `requested` entry），因此权威路径单独补 id 即可与 `approval.decided` 配对，**无跨路径错配风险**。

## 改动点

### ① `backend/internal/runtime/runtime.go` — handler + `appendApproval`

现状（`Create` 失败也照发事件；已有 `FindPendingByToolCall` 幂等前置守卫）：

```go
_ = r.deps.Approvals.Create(bg, &domain.Approval{ ... })
r.append(bg, runID, domain.EvtApprovalRequested, toolName+" requires approval")
```

改为——先把记录建好拿到 `a.ID`，**Create 成功才发事件**（避免发无 `approval_id` 的噪音事件），并新增 `appendApproval` helper 带三字段：

```go
a := &domain.Approval{
    RunID: runID, ToolCallID: toolCallID, ToolName: toolName,
    ArgsHash: itool.ArgsHash(args), RequestedBy: ownerID,
    Reason:    "high-risk tool call requires approval",
    ExpiresAt: time.Now().Add(30 * time.Minute).UnixMilli(),
}
if err := r.deps.Approvals.Create(bg, a); err == nil {
    r.appendApproval(bg, runID, a)
}
// else: 该文件既有惯例是 `_ =` 吞错、无 logger；Create 失败则跳过事件（无 id 不可配对）

// 新 helper（与现有 append 并列，runtime.go:166 附近）
func (r *Runtime) appendApproval(ctx context.Context, runID string, a *domain.Approval) {
    payload, _ := json.Marshal(map[string]string{
        "text":        a.ToolName + " requires approval",
        "approval_id": a.ID,
        "tool_name":   a.ToolName,
    })
    _, _ = r.deps.Events.Append(ctx, runID, domain.EvtApprovalRequested, string(payload), "normal")
}
```

### ② `backend/internal/service/run_service.go` — decided payload

`DecideApproval` 的 `a`（含 `ToolName`）已在 `a, err := s.approvals.Get(ctx, in.ApprovalID)`（`run_service.go:144`）处取得，line 158 作用域内可用。替换：

```go
// 现状（run_service.go:158）
_, _ = s.events.Append(ctx, in.RunID, domain.EvtApprovalDecided,
    `{"approved":`+boolStr(in.Approve)+`}`, "normal")
```

改为：

```go
payload, _ := json.Marshal(map[string]any{
    "approved":    in.Approve,      // 必须 JSON bool，不能是字符串 "true"
    "approval_id": in.ApprovalID,
    "tool_name":   a.ToolName,
    "reason":      in.Reason,        // 可为空串；前端 rejected 行按需显示
})
_, _ = s.events.Append(ctx, in.RunID, domain.EvtApprovalDecided, string(payload), "normal")
```

配套：
- 新增 import `encoding/json`（该文件当前未 import）。
- 删除 `boolStr`（`run_service.go:166`）——改后无任何引用，避免死代码（已核实 `boolStr` 仅此一处使用）。

**关键坑**：`approved` 必须是 JSON 布尔（`true`/`false`），不能是字符串 `"true"`。前端 `parseApprovalPayload` 用 `p.approved === true` 严格判断（`chat-messages.ts:200`），字符串 `"true"` 会被判为 rejected。故 decided payload 用 `map[string]any`（`approved` 放 bool），而 requested payload 三字段全字符串，可用 `map[string]string`。

## 明确不做（YAGNI）

- **副信号路径**（`runner_eino.go:277/302`）不改——无 `approval_id`，靠前端守卫滤除。
- **显式 expired 决策**（`run_service.go:151-153` 提前 return、不发事件）保持现状——前端"run 终态 + undecided → expired"派生（上轮 Task 6）已覆盖"run 死了审批还挂着"这个更常见情形。
- **`GET /runs/{id}/approvals` 读端点**（scope B）不做——留 follow-ups；本轮刷新/直接打开 run 靠 SSE 重放已持久化的 enriched 事件。
- **前端任何代码**零改动。
- 不引入新日志手段（`runtime`/`service` 包惯例是 `_ =` 吞错 + `observability.Incr*` metrics，无 slog；Create 失败静默跳过事件，符合该文件既有风格）。

## 数据流

```
高风险工具调用
  → runtime.go onNeedApproval handler
  → Approvals.Create(a)  成功 → a.ID 已生成
  → appendApproval: 发 approval.requested {"text","approval_id","tool_name"}   (SSE 推送 + 持久化)
  → 前端 parseApprovalPayload 读到 approval_id/tool_name → 建 requested entry → 渲染 pending 卡
用户 Approve / Reject
  → run_service.go DecideApproval(a := Get(approval_id))
  → Approvals.Decide (幂等：仅 pending 可写一次)
  → 发 approval.decided {"approved"(bool),"approval_id","tool_name","reason"}
  → 前端派生 approved/rejected → 卡折叠成单行
  → Approve 则 Resume（collectApprovals 复用 a.ToolName+ArgsHash），Reject 则 run failed
```

SSE 事件持久化于 `agent_events` 表（`EventRepo.Append`），`handleEvents` 按 seq 支持 `Last-Event-ID`/`after` 重放（`server.go:232-313`），因此刷新/直接打开一个已停在 `waiting_approval` 的 run，重放 enriched 事件即可重建 pending 卡，无需额外读端点。

## 错误处理

| 场景 | 行为 |
|---|---|
| `Approvals.Create` 失败（`runtime.go` handler） | 不发 `approval.requested`（无 `a.ID`，发出去前端也配不上对）；与文件既有 `_ =` 吞错风格一致 |
| `Approvals.Decide` 已决定/过期（`run_service.go`） | 既有 `ErrConflict`/`approval_expired` 路径不变，本 spec 不触碰 |
| `in.Reason` 为空串（Approve 无理由） | payload `reason` 为空串；前端 approved 单行不显示 reason，符合预期 |
| 副信号路径的 `approval.requested`（无 `approval_id`） | 前端 `if (p.approvalId)` 守卫跳过，不建 entry、不配对 |
| `approval.decided` 的 `approval_id` 无对应 `approval.requested`（孤儿事件） | 前端 `deriveApprovals` 只遍历 `requested` Map 的 key（`chat-messages.ts` `deriveApprovals`），孤立的 decided 不建 entry、静默丢弃，无 UI 垃圾 |
| 同一 tool_call 重复中断（`FindPendingByToolCall` 命中，`runtime.go:91`） | 命中守卫直接 `return`，**不重发** `approval.requested`（首条已持久化）；保持既有幂等语义 |

## 测试（全部后端；前端零改动）

扩展现成集成测试 `backend/tests/integration/approval_test.go`（`testutil.Harness` 暴露 `h.Events *store.EventRepo`、`h.Approvals *store.ApprovalRepo`；`EventRepo.ListAfter(ctx, runID, 0, 500)` 返回 `[]domain.AgentEvent`，含 `.Type`/`.PayloadJSON`）：

| 用例 | 断言 |
|---|---|
| 扩展 `TestApprovalApproveResume` | `h.Events.ListAfter` 拉事件 → 找 `EvtApprovalRequested`：`json.Unmarshal` payload，断言 `approval_id == a.ID`、`tool_name == a.ToolName`；找 `EvtApprovalDecided`：断言 `approved == true`（**JSON bool**，非字符串）、`approval_id == a.ID`、`tool_name`、`reason == "looks good"` |
| 扩展 `TestApprovalReject` | 同上，decided 事件断言 `approved == false`、`reason == "nope"` |
| 回归 | 两测试既有的 run 终态断言（succeeded / failed + `approval_rejected`）不变 |

测试通过即证明 payload 契约成立；前端 `parseApprovalPayload`/`ApprovalCard.parseApproval` 因按这些 key 预写，无需改动。

## 验收

- `cd backend && go test ./...`（或 `make test`）全绿，含扩展后的 `approval_test.go`。
- `go vet ./...` 无新增告警；`boolStr` 已删无残留引用。
- 端到端 smoke（如本环境有模型 endpoint）：触发高风险工具 → 审批卡渲染出（含工具名 + Approve/Reject）→ 点 Approve → run 继续、卡折叠成单行。无模型 endpoint 时以集成测试证明 payload 契约，smoke 标注视觉受限。
- 前端零改动（`git diff` 应不含 `frontend/`）。
