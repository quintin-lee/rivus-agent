# 设计文档：前端交互方式优化 —— 完全通过对话框（聊天 + 弹窗）交互

- 日期：2026-10-10
- 状态：待实现
- 方案：A（Session 即对话，消息流重构）

## 1. 背景与问题

当前前端布局为三段堆叠：

- 左侧 `SessionPanel`：会话列表 + 新建/删除 + Run 历史。
- 主区 `RunForm`（表单化创建 Run）+ `RunDetail`（状态/Steps/结果）+ `EventStream`（SSE 事件流）。

问题：

- 心智模型偏向"表单 + 仪表盘"，而非"与 Agent 对话"。
- 用户需要理解 RunForm 的 mode/约束/成功标准/预算等概念，且这些与"提交任务"混在一个大表单里。
- 审批、Steps 进度、事件流、结果分布在不同区域，用户需要来回切换看全局。

## 2. 目标与非目标

### 目标（YAGNI 剪裁）

- 主界面 = 单一聊天面板：底部输入框 + 消息流。
- 复杂配置（mode/约束/成功标准/预算）收进弹窗 `NewRunDialog`，默认全部走服务端默认值。
- 审批卡片、Steps 进度、事件流、最终结果全部内联到聊天消息流。
- 保留会话列表（瘦侧边栏）与 Header 设置按钮（模型设置弹窗）。
- 无后端 API 变更；纯前端重构。审批操作复用现有 `api.decideApproval(runId, approvalId, approve, reason?)`。

### 非目标

- 不做自然语言命令解析（`/mode plan_execute` 之类）。用户选择"极简输入 + 高级弹窗"而非"聊天内指令"。
- 不做流式打字机效果的 goal 输入（提交一次即生成一条 user 气泡）。
- 不做多人协同 / 实时在线状态。
- 不做消息持久化到本地（刷新后通过 `loadRunHistory` 重建）。
- 不引入新前端依赖（复用现有 Radix Dialog、zustand、react-markdown）。

## 3. 用户决策记录

| 问题 | 选择 |
|---|---|
| 对话框具体含义 | 混合：聊天 + 弹窗 |
| Run 参数处理 | 极简输入 + 高级弹窗 |
| 审批/进度/结果呈现 | 全部内联到聊天流 |
| 侧边栏/设置 | 瘦侧边栏 + 保留设置（Header） |
| 方案 | A：Session 即对话，消息流重构 |

## 4. 架构与组件边界

### 4.1 新布局

```
┌────────────────────────────────────────────────────────┐
│ Header（SSE 状态、api 可达性、Settings 按钮 → 模型设置） │
├──────────┬─────────────────────────────────────────────┤
│ 瘦侧边栏  │  ChatPanel                                 │
│ 会话列表  │   ┌──────────────────────────────────────┐ │
│ 新建/删除 │   │  ChatMessageList（滚动，欢迎气泡）    │ │
│          │   │   · user 气泡（goal）                  │ │
│          │   │   · system 气泡（status/Steps/events） │ │
│          │   │   · approval 卡片（内联）              │ │
│          │   │   · assistant 气泡（result）           │ │
│          │   └──────────────────────────────────────┘ │
│          │  ChatInputBar（goal + 发送 + 高级按钮）      │
└──────────┴─────────────────────────────────────────────┘
```

### 4.2 组件清单

**删除**（主区堆叠组件）：

- `RunForm`（表单逻辑迁移至 `NewRunDialog`）
- `RunDetail`（拆分为 system 气泡内的 status/Steps + assistant 气泡内的 result）
- `EventStream`（拆分为 system 气泡内的 events 折叠）
- `SessionPanel` 主区堆叠职责被 `SessionList` 替代

**保留/微调**：

- `Header`（保留）
- `SettingsDialog`（保留，挂在 Header 右侧）
- `ApprovalCard`（保留为消息内联渲染器，新增受控 `onApprove/onReject` 回调，去掉独立状态跟踪）

**新建**：

- `SessionList`：会话列表 + 新建 + 删除（复用现有 `session-store` 与删除 Dialog）；移除 Run History 部分（Run 历史融入聊天）。
- `ChatPanel`：容器 = `ChatMessageList + ChatInputBar`；负责空会话欢迎态。
- `ChatMessageList`：渲染 `ChatMessage[]`；每条消息按 `kind` 分发到对应消息组件。
- `ChatInputBar`：`goal Textarea + 发送 + 高级按钮`；空 goal 或无 activeSession 时禁用发送；高级按钮打开 `NewRunDialog`。
- `NewRunDialog`：Radix Dialog；字段 = mode 切换、约束列表、成功标准列表、预算输入；校验逻辑复用 `lib/budget.ts` 的 `assembleBudget` 与 `BUDGET_FIELDS`；确定时回调 `onSubmit({ goal, mode, constraints, successCriteria, budget })`。
- `Messages/UserBubble`：渲染 goal + 提交状态（pending/sent/failed）。
- `Messages/AssistantBubble`：渲染 result，复用 `lib/result-render.ts` + `ResultViewBlock`。
- `Messages/SystemStatusBubble`：渲染 run status + 可折叠 Steps（`activeSteps`）+ 预算使用；run 终态时展示"重试"或"取消"按钮（按需）。
- `Messages/SystemEventsBubble`：渲染事件流，默认折叠，可展开；复用 `EventStream` 内部渲染逻辑。
- `Messages/ApprovalBubble`：包裹 `ApprovalCard`，SSE 驱动更新审批状态。

### 4.3 消息模型

```ts
type ChatMessage =
  | { kind: 'user'; id: string; runId: string; goal: string; status: 'pending' | 'sent' | 'failed' }
  | { kind: 'assistant'; id: string; runId: string; resultJson?: string; error?: { code: string; summary?: string } }
  | { kind: 'system-status'; id: string; runId: string; status: RunStatus; steps: Step[]; budget: Budget }
  | { kind: 'system-events'; id: string; runId: string; events: AgentEvent[]; collapsed: boolean }
  | { kind: 'system-approval'; id: string; runId: string; approval: Approval; state: 'pending' | 'approved' | 'rejected' }
```

派生来源（不新增后端接口）：

- `runHistory`（`run-store`）：每个 Run 生成一条 `user` + 一条 `system-status`（终态时加一条 `assistant`）。
- `activeSteps`：注入到对应 Run 的 `system-status`。
- SSE 事件（`useEventStream`）：实时追加到对应 Run 的 `system-events`；`run_status_changed` → `waiting_approval` 时生成 `system-approval`。
- 审批操作（`api.decideApproval(runId, approvalId, approve, reason)`）：更新对应 `system-approval.state`，同一气泡内联变化，不另起消息。

新增 `store/chat-store.ts`（可选，YAGNI）：若直接由 `run-store + event-stream` 派生足够，则不加新 store；消息派生函数放 `lib/chat-messages.ts` 保持纯函数可测。

**推荐**：不加 `chat-store`；`lib/chat-messages.ts` 导出 `deriveMessages(runHistory, activeRun, activeSteps, eventsByRun, approvalsByRun, pendingGoal): ChatMessage[]`，由 `ChatPanel` 用 `useMemo` 计算。

## 5. 数据流

### 5.1 提交 Run（聊天内）

1. `ChatInputBar` 输入 goal；点高级 → `NewRunDialog`；或直接发送（全默认值）。
2. 点击发送 → 调 `onSubmit({ goal, ... })`。
3. `run-store.submitRun`（新 action）：
   - 乐观追加 `pendingGoal` 到临时队列，生成临时 user 气泡（`status: 'pending'`）。
   - 调 `api.createRun(req, idempotencyKey)`；成功 → `activateRun(runId)`；`pendingGoal` 标记 `sent` 并从队列移除。
   - 失败 → `pendingGoal.status = 'failed'` + `error` 字段，气泡保留 goal 供重试；不清空输入。
4. 后续状态/Steps/事件由 SSE 驱动，追加/更新对应 Run 的消息。

### 5.2 切换会话

1. `switchSession(sessionId)` → `loadRunHistory(sessionId)`。
2. `ChatMessageList` 按 `run.created_at` 排序重建；空会话显示欢迎气泡（示例 goal + 快速填充按钮，非必需，YAGNI 可省）。
3. 之前会话的 `activeSteps` / `events` 重置（只跟踪 active session）。

### 5.3 审批

1. SSE `run_status_changed → waiting_approval`：生成 `system-approval` 气泡。
2. 用户点批准/拒绝 → `api.decideApproval(runId, approvalId, approve, reason)`；气泡内联更新为 `approved`/`rejected`（乐观更新，失败回滚并展示错误）。
3. SSE 继续推 status/steps/events，同一 run 的消息流继续追加。

### 5.4 断线重放

- `useEventStream` 保留 `Last-Event-ID` 机制。
- 断线时 `ui-store.sseStatus` 变化 → `ChatMessageList` 顶部（或系统气泡）显示"重连中…"；重连后按 `Last-Event-ID` 重放，不丢消息。

## 6. 错误处理

- **创建 Run 失败**：user 气泡显示 `failed` 红色边框 + 错误信息 + 重试按钮；保留 goal 文本，不清空输入。
- **预算校验**：`NewRunDialog` 内即时校验，复用 `assembleBudget` 返回值 `undefined` 判断；非正整数显示"需正整数，已忽略"（与现有 RunForm 一致）。
- **审批过期 / 运行中删除会话**：删除 Dialog 内联提示（复用现有 `deleteHasActive` 逻辑）；审批气泡收到 409/过期时显示"已过期"并禁用按钮。
- **SSE 断连**：顶部系统横幅，重连成功后自动消失。
- **result 渲染异常**：`ResultViewBlock` 已有 try/catch，保持"raw" 兜底。

## 7. 测试策略

- 保留 `session-store.test.ts`、`run-store.test.ts`、`ui-store.test.ts`；`run-store` 新增 `pendingGoal` 队列与 `submitRun` action 需补充测试。
- 新增 `lib/chat-messages.test.ts`：`deriveMessages` 纯函数测试（空会话、单 run 进行中、run 终态、审批 pending、断线重连事件重放、同一 run 多轮审批按 seq 排序）。
- 新增 `ChatMessageList.test.tsx`：按 `kind` 渲染正确；审批气泡交互；折叠/展开；同一 run 多 status/system 气泡合并。
- 新增 `ChatInputBar.test.tsx`：空禁用、提交后清空、失败保留 goal。
- 新增 `NewRunDialog.test.tsx`：默认全留空提交（走服务端默认）、mode 切换、约束/标准增删、预算校验。
- 迁移/删除：`RunForm.test.tsx`、`EventStream.test.tsx` 的有用断言迁到 `NewRunDialog.test.tsx` 与 `SystemEventsBubble` 内；删除独立区域测试。
- 验收（手动）：浏览器里走一次完整 Run（含一次审批）+ 中途断网重连 + 切换会话历史重建。

## 8. 文件变更清单

| 文件 | 操作 |
|---|---|
| `frontend/src/App.tsx` | 重写：`Header + SessionList + ChatPanel` |
| `frontend/src/components/SessionPanel.tsx` | 删除，改为 `SessionList.tsx`（新建） |
| `frontend/src/components/RunForm.tsx` | 删除，逻辑迁移至 `NewRunDialog.tsx` |
| `frontend/src/components/RunDetail.tsx` | 删除，拆分到 `Messages/SystemStatusBubble.tsx` + `Messages/AssistantBubble.tsx` |
| `frontend/src/components/EventStream.tsx` | 删除，拆分到 `Messages/SystemEventsBubble.tsx` |
| `frontend/src/components/ActionBar.tsx` | 复用/删除，看实际是否被 `SystemStatusBubble` 替代 |
| `frontend/src/components/ChatPanel.tsx` | 新建 |
| `frontend/src/components/ChatInputBar.tsx` | 新建 |
| `frontend/src/components/NewRunDialog.tsx` | 新建 |
| `frontend/src/components/Messages/*.tsx` | 新建 5 个消息组件 |
| `frontend/src/lib/chat-messages.ts` | 新建：`deriveMessages` |
| `frontend/src/store/run-store.ts` | 新增 `pendingGoal` 队列 + `submitRun` action |
| `frontend/src/components/ApprovalCard.tsx` | 调整为受控组件，新增 `onApprove/onReject` 回调 |
| 测试文件 | 见第 7 节 |

## 9. 迁移与回滚

- 一次性切换，不做特性开关；如回滚保留 `RunForm/RunDetail/EventStream` 旧组件文件不删（标记 deprecated），或直接从 git revert。
- 无数据迁移（消息流全部派生自现有 store 与 SSE）。

## 10. 成功标准

- 主界面只有"会话列表 + 聊天面板 + 输入框"三块，无表单化区域。
- 用户可从输入框提交 Run 并看到 user 气泡；审批/进度/事件/结果全部出现在同一 run 的消息序列内。
- 高级配置仅在 `NewRunDialog` 内可改，默认全留空即走服务端默认。
- 断网重连后不丢消息；切换会话后历史消息完整重建。
- `tsc && vite build` 通过；`vitest` 全绿；`eslint` 无新告警。
