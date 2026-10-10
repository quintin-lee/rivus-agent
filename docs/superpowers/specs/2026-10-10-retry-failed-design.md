# 失败重试设计文档

- 文档版本：v1.0
- 日期：2026-10-10
- 目标：failed 的 Run 可一键原地重试

---

## 1. 背景

后端 `Resume` 虽放行 `failed`，但 `ValidTransition` 禁止一切终态回 `running`，失败重试实际调不通。本次补上该缺口。

## 2. 后端

- 状态机加唯一特例：`failed → running` 允许，其余终态仍禁回。`UpdateStatus` 终态铁律不动，`Resume` 对 failed 走专用分支。
- 重试时 `attempt+1`，清空 `error_code/error_summary`，写 `run.resumed` 事件（含 attempt）。
- `timed_out/cancelled/succeeded` 调 resume 仍 409。

## 3. 前端

- ActionBar 仅 `failed` 出“重试”按钮（复用 paused 的 Resume 样式），调现有 `resumeRun` + 刷新；409 显示“当前状态不可重试”。
- vitest：failed 显示按钮且点击调接口；succeeded 不显示按钮。

## 4. 验收

- 后端单测：failed→running 合法，其余终态仍拒绝；attempt 递增、错误清空。
- 集成测试：失败 Run 调 resume 后重新执行并终态。
- 前端 `tsc` + vitest + `vite build` 通过。

## 5. 非目标

- timed_out/cancelled 的 clone 重跑；自动重试；退避策略。
