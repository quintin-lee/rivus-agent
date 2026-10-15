# 审批读端点 `GET /runs/{id}/approvals` — 设计文档

- **日期**: 2026-10-15
- **范围**: 纯后端。`backend/internal/api/http/server.go`（1 路由 + 1 handler）、`backend/internal/service/run_service.go`（1 方法）、集成测试。前端零改动（可选后续再 wire）。
- **状态**: 设计已确认。
- **上级 spec**: 兑现 `2026-10-14-follow-ups.md` §1 的读端点半（事件 payload 半已在上轮完成）。

## 背景 / 缺口

上轮补了 `approval.requested`/`approval.decided` 事件 payload（带 `approval_id`/`tool_name`），前端**刷新后**靠 SSE 重放即可重建 pending 卡。但**直接打开一个已停在 `waiting_approval` 的 run**（刷新、或事件窗口未覆盖）时，前端没有独立数据源拉当前 pending 审批。`ApprovalRepo.ListByRun` 底层已存在（`approval_repo.go:62`，含全字段 + `created_at` 排序），缺的只是一个 HTTP 端点把它吐出来。

## 改动

### `backend/internal/service/run_service.go` — 新方法

```go
// ListApprovals 返回某 run 的全部审批记录（按 created_at 升序），供前端 bootstrap pending 卡。
func (s *RunService) ListApprovals(ctx context.Context, ownerID, runID string) ([]domain.Approval, error) {
    if _, err := s.tasks.GetRun(ctx, ownerID, runID); err != nil {
        return nil, err
    }
    return s.approvals.ListByRun(ctx, runID)
}
```

先 `tasks.GetRun` 做租户归属校验（防越权读他人 run 的审批，与 `DecideApproval` 的 `GetRun` 一致），再 `ListByRun`。

### `backend/internal/api/http/server.go` — 路由 + handler

- 路由（`routes()`）：`s.mux.HandleFunc("GET /api/v1/runs/{id}/approvals", s.handleListApprovals)`。
- handler：
- 404 判定用 `sql.ErrNoRows`（`tasks.GetRun` 对不存在的 run 直接返回 `QueryRowContext` 的原始 `sql.ErrNoRows`，**非** `domain.ErrNotFound`，见 `task_repo.go` `GetRun`）+ `domain.ErrForbidden`（非 owner 读他人 run）；其余错误走 500。`sql`/`errors`/`domain` 均已在 `server.go` import。

```go
func (s *Server) handleListApprovals(w http.ResponseWriter, r *http.Request) {
    id := r.PathValue("id")
    approvals, err := s.runs.ListApprovals(r.Context(), ownerOf(r), id)
    if err != nil {
        if errors.Is(err, sql.ErrNoRows) || errors.Is(err, domain.ErrForbidden) {
            writeErr(w, 404, "not_found", "run not found", false)
            return
        }
        writeErr(w, 500, "internal", "list approvals failed", true)
        return
    }
    writeJSON(w, 200, map[string]any{"approvals": approvals})
}

## 明确不做（YAGNI）
- 不加分页 / 状态过滤 / `?status=pending` 查询参数（`ListByRun` 全量 + 前端过滤足够）。
- 不 wire 前端 `api.listApprovals` + 气泡调用（本轮纯后端交付端点；前端接线是独立小项，follow-up 或下轮做）。
- 不改 `Server` 构造签名（`runs *service.RunService` 已在）。

## 数据流

```
GET /api/v1/runs/{id}/approvals
  → ownerOf(r) 取 owner
  → RunService.ListApprovals: tasks.GetRun(owner, id) 校验归属 → approvals.ListByRun(id)
  → 200 {"approvals":[Approval…]} 或 404
（前端后续: 激活 waiting_approval run 时调此端点 → 合并进 deriveMessages 的 approval 来源）
```

## 错误处理

| 场景 | 行为 |
|---|---|
| run 不存在 | `tasks.GetRun` 返回 `sql.ErrNoRows` → 404 |
| 非本 owner 的 run | `tasks.GetRun` 返回 `domain.ErrForbidden` → 404 |
| run 存在但无审批 | `{"approvals":[]}` 200 |
| DB 读失败 | 500 `retryable:true` |

（`sql.ErrNoRows` 与 `domain.ErrForbidden` 合并走 404，避免泄露他人 run 是否存在；`ListByRun` 的 DB 读失败仍走 500。）

## 测试

扩展 `tests/integration/approval_test.go`（或新建 `approval_list_test.go`，同 package）：
| 用例 | 断言 |
|---|---|
| 审批后 `GET /runs/{id}/approvals` | 200，`approvals` 含 1 条，`id`/`tool_name`/`status` 与 `waitApproval` 拿到的记录一致 |
| 无审批的 run | 200 `{"approvals":[]}`（非 `null`） |
| 不存在的 run id | 404 |
| 非 owner 读他人 run（`X-Owner-ID: intruder`）| 404（跨租户校验生效）|

复用 `testutil.Harness.Do("GET", "/api/v1/runs/"+id+"/approvals", nil, nil)`。

## 验收

- 端点 200/404 行为正确；返回全字段 `[]Approval`；空时为 `[]` 非 `null`。
- 租户归属校验生效（非 owner 读他人 run → 404）。
- 前端零改动（`git diff` 不含 `frontend/`）。
- `go build ./...` + `go vet ./...` + `go test ./tests/integration/` 全绿。
