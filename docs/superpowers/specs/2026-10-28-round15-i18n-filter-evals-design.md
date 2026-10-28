# 第 15 轮设计：前端 i18n 收官 + 运行历史过滤 + evals 扩展

日期：2026-10-28　|　状态：已批准（用户 'ok'）　|　前置：R1–R14 全部完成

## 1. 背景与缺口（已核实）

1. **i18n 残留**：R13/R14 迁移了大部分组件，仍剩 3 处硬编码——`SubagentCard.tsx`（L40 `加载失败`、L61 `子 agent ×{count}`）、`ToolCallsCard.tsx`（L22 `工具调用 ×{count}`）、`SettingsDialog.tsx`（L67 `保存失败`、L137 `保存中…`/`保存`）。NewRunDialog/ApprovalCard/SystemStatusBubble 无中文残留（仅 aria-label，属既有键）。
2. **react-hooks lint 缺口**（R11 遗留 YAGNI）：`frontend/eslint.config.js`（flat config：js.configs.recommended + @typescript-eslint 8.71.1 recommended，no-undef off）没有 react-hooks 插件；`eslint-plugin-react-hooks` 未安装。
3. **运行历史无过滤**：`GET /api/v1/runs` 支持 `session_id`（可选）与 `limit`，但无 `status` 过滤——idx_runs_status(status, created_at) 索引已存在却未被 API 使用；跨会话历史弹窗只能全量翻找。
4. **evals 盲区**：24 场景未覆盖 R14 的 run 详情三端点（getRun/events/approvals 响应形状）、未覆盖审批**拒绝**路径（至今只测过批准，拒绝语义 = RunFailed + `approval_rejected`，integration/approval_test.go:137 已核实）、批2 新特性也需要端到端场景。

## 2. 方案总览

| 批次 | 内容 | 规模 |
|---|---|---|
| 批1 | i18n 最后 3 组件迁移 + react-hooks 插件入 lint 门禁 | 前端 5-6 文件 |
| 批2 | `GET /runs?status=` 服务端过滤（两分支）+ RunsHistoryDialog 筛选 chips | 后端 3 文件 + 前端 2 文件 |
| 批3 | evals 24→27：run_detail_api / runs_status_filter / approval_rejected_flow | 1 新测试文件 |

**YAGNI**：不做 8 状态全量 chips（前端只放 常用 5 个：全部/成功/失败/取消/超时）；不做分页 cursor；不做 evals MCP-HTTP 端到端场景（harness MCP 为 stub，真实拉起 Manager 成本高，单元+集成已覆盖）；不改 approve/reject 语义。

## 3. 批次详情

### 3.1 批1 i18n 收官 + react-hooks

- 新键（zh/en 同步）：`subagent.cardTitle`（子 agent ×{n} / Sub-agents ×{n}）、`toolcalls.cardTitle`（工具调用 ×{n} / Tool calls ×{n}）、`settings.saveFailed`（保存失败 / Failed to save）、`settings.saving`（保存中… / Saving…）、`settings.save`（保存 / Save）。复用已有 `common.loadFailed`。
- 迁移模式沿用 R13：裸 `useUiStore((s) => s.lang)` 订阅语句 + `t()` 调用；`{n}` 占位符。
- react-hooks：`npm i -D eslint-plugin-react-hooks --legacy-peer-deps`（R11 教训）；flat config 增 plugins + `rules-of-hooks: 'error'`（挡门禁）+ `exhaustive-deps: 'warn'`（不阻塞）；跑 `npm run lint` 修掉新增 error 级违规。
- 测试：受影响组件既有断言（中文字符串）同步更新；至少 1 个组件加 en 态用例。

### 3.2 批2 运行历史状态过滤

- **后端**（TDD）：
  - `handlers_runs.go handleListRuns`：读 `status` query——空 = 原行为（不过滤）；值必须是 8 个 `domain.RunStatus` 之一（queued/running/waiting_approval/paused/succeeded/failed/cancelled/timed_out），非法 → 400 bad_request；透传给两个分支。
  - `task_repo.go`：`ListRunsByOwner(ctx, owner, limit)` → `ListRunsByOwner(ctx, owner, status string, limit)`；`ListRunsBySession` 同加；`status != ""` 时 SQL 追加 `AND status = ?`。调用方仅 handler 一处（grep 核实）。
- **前端**（TDD）：
  - `api.ts listRuns(sessionId?, limit=50, status?)`：status 存在才拼 `&status=`。
  - `RunsHistoryDialog`：标题下加 chips 行（全部/成功/失败/取消/超时），激活态高亮，点击重拉；沿用 fetch-on-open + error/retry 结构。
  - 测试：api.test.ts 拼参断言；RunsHistoryDialog.test.tsx chip 点击 → listRuns 收到 status。

### 3.3 批3 evals 24→27（scenarios_round15_test.go）

1. `run_detail_api`（category tool）：普通成功 run → PostCheck 三连断言：`GET /runs/{id}` 200 + run.id 匹配；`GET /runs/{id}/events?after=0` 200 + 事件数组含 run.started；`GET /runs/{id}/approvals` 200（空审批列表）。
2. `runs_status_filter`（category agent）：主 run 走预算上限失败（复用 R14 output_bytes 或 tool_calls 手法）→ PostCheck 里再造一个成功 run（同 session EnqueueText + CreateRun + WaitTerminal）→ `GET /api/v1/runs?status=failed` 只含失败 run、`?status=succeeded` 只含成功 run、无过滤参数两者都在。
3. `approval_rejected_flow`（category tool）：条件审批工具（如 publish_draft 风格 write 工具）+ MidRun `POST approvals/{aid} {"approve":false}` → MidRun 内二次 WaitTerminal → Want failed；PostCheck 断言 ErrorCode == `approval_rejected`。

**验收（DoD）**

1. 后端 `go build ./... && go vet ./...` 通过；`go test ./... -count=1` 全绿；`make test-eval` 27 场景 pass_rate 1。
2. 前端 `npx tsc --noEmit` 0 错误；`npm run lint` 通过（含 react-hooks 规则）；`npx vitest run` 全绿。
3. docs zh/en：README 评估计数 27、前端 README 组件行/筛选说明。
4. spec §6 偏差回写。

## 5. 风险

- `exhaustive-deps` 若存量违规多 → warn 级不阻塞门禁，只修 error 级；如 error 级存量违规集中，修复范围限定在本批触碰文件。
- status 过滤改变 repo 签名 → 仅 1 处调用方，编译器兜底。
- evals 双 run 场景依赖 FIFO 队列顺序（run2 的 EnqueueText 排在 run1 消费完之后）→ PostCheck 时序天然满足（run1 已终态）。

## 6. 执行偏差

（执行后回写）
