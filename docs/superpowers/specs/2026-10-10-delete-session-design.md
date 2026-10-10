# 删除会话设计文档

- 文档版本：v1.0
- 日期：2026-10-10
- 目标：会话可删除，名下数据级联清理，运行中任务受保护

---

## 1. 决策

- 语义：级联全删（runs/事件/审批/步骤/checkpoint），不可恢复，二次确认。
- 运行中（queued/running/waiting_approval/paused）Run 存在时 409 拒绝，先取消再删。
- 非本人会话返回 404（不暴露存在性）。

## 2. 后端

- `DELETE /api/v1/sessions/{id}`。
- 归属校验失败 → 404 `not_found`；有运行中 Run → 409 `conflict`；成功 → 200 `{"status":"deleted"}`。
- 单事务删除顺序：`agent_events` → `approvals` → `run_steps` → `runs` → `sessions`，再 `DeleteByRunPrefix` 清 checkpoint。
- 需新增 `store` 方法：`DeleteSession(ctx, ownerID, sessionID)`（内含运行中检查，返回哨兵错误区分 404/409）。

## 3. 前端

- SessionPanel 行 hover 删除图标 → 确认 Dialog（显示 Run 数量 + 不可恢复文案；运行中存在时禁用按钮并提示）。
- 成功：列表移除；若为当前会话，切首个剩余会话并清空右侧（复用现有 switch + activeRun 置空路径）。
- `api.ts` 加 `deleteSession`；vitest 覆盖确认框、接口调用、当前会话切换。

## 4. 验收

- 后端集成测试：级联后各表无残留；运行中 409；跨 owner 404；重复删 404。
- 前端 `tsc` + vitest 通过；有运行中时按钮 disabled。
- `go vet` / `vite build` 通过。

## 5. 非目标

- 批量删除；回收站/软删除；Run 单条删除。
