# Rivus Agent Frontend 设计文档

- 文档版本：v1.0
- 日期：2026-10-09
- 目标：为 Rivus Agent 后端提供功能完整、操作路径短的 Web UI
- 布局形式：单页 Dashboard + 侧边栏（方案 A）
- 技术栈：React 18 + Vite + TypeScript + shadcn/ui + Tailwind CSS

---

## 1. 范围

### 1.1 包含功能

| 功能 | 说明 |
|---|---|
| 会话管理 | 创建会话、切换 active session、显示会话元数据 |
| 任务提交 | 填写 goal / mode / constraints / success criteria / budget，提交 Run，带 `Idempotency-Key` |
| Run 详情 | 状态 badge、步骤 timeline、verification 结果、result_json 摘要 |
| SSE 事件流 | 实时展示 model / tool / step / run / approval 事件，终端风格渲染 |
| 审批操作 | 在事件流内直接批准/拒绝，带 reason 输入 |
| 取消/恢复 | 依据当前 Run 状态动态显示操作按钮 |
| 历史 Run 列表 | 侧边栏展示当前 session 下所有 Run，点击切换 |
| 连接状态 | Header 显示 SSE 连接状态和 API 可达性 |

### 1.2 不包含功能

- 用户登录 / 多租户切换（后续由后端 `X-Owner-ID` 注入）
- 文件上传 / 下载
- 多语言 i18n
- 移动端适配（桌面端为主）
- E2E 测试（Playwright）

---

## 2. 技术选型

| 类别 | 选择 | 说明 |
|---|---|---|
| 构建 | Vite 7 | 开发热重载，生产构建 |
| 框架 | React 18 + TypeScript | 组件化，类型安全 |
| 状态管理 | Zustand | 轻量，store 按领域拆分（session / run / ui） |
| UI 组件 | shadcn/ui + Tailwind CSS 3 | 组件可本地化定制，不引入大型组件库依赖 |
| 图标 | lucide-react | 轻量 SVG 图标 |
| SSE 客户端 | 自研 `fetch + ReadableStream` 封装 | 原生 `EventSource` 不支持自定义 Bearer header |
| 包管理 | pnpm | 快，磁盘效率高 |
| 测试 | Vitest + React Testing Library | 组件单测，不引入 E2E |
| 代码规范 | Prettier + ESLint（eslint-plugin-react） | 格式化 + 基础 lint |

---

## 3. 项目结构

```
frontend/
├── public/
├── src/
│   ├── main.tsx
│   ├── App.tsx                    # 根组件，组装所有子模块
│   ├── lib/
│   │   ├── api.ts                 # ApiClient 类 + ApiError
│   │   ├── sse.ts                 # SSE 订阅 hook（useEventStream）
│   │   ├── types.ts               # 与后端 domain 对应的 TS 类型
│   │   └── utils.ts             # cn() 等工具函数（shadcn 约定）
│   ├── store/
│   │   ├── session-store.ts       # Zustand: sessions / activeSessionId
│   │   ├── run-store.ts           # Zustand: activeRun / steps / events / eventFilter
│   │   └── ui-store.ts            # Zustand: eventStreamOpen / sidebarCollapsed / connectionStatus
│   ├── components/
│   │   ├── ui/                    # shadcn/ui 组件（Button, Card, Badge, Dialog, Input,
│   │   │                           Select, ScrollArea, Tabs, Tooltip, Skeleton...）
│   │   ├── Header.tsx             # 项目名 + 连接状态 indicator
│   │   ├── SessionPanel.tsx       # 左侧：会话列表 + 新建 + 历史 Run 列表
│   │   ├── RunForm.tsx            # 任务提交表单（可折叠高级选项）
│   │   ├── RunDetail.tsx          # 状态 badge + Steps Timeline + Verification
│   │   ├── EventStream.tsx        # 终端风格事件流（过滤 chips + 搜索）
│   │   ├── ApprovalCard.tsx       # 审批卡片（内嵌在 EventStream 中）
│   │   └── ActionBar.tsx          # 取消 / 恢复 / 查看审批 操作按钮
│   └── App.config.ts             # API base URL、owner ID 默认值
├── tailwind.config.ts
├── tsconfig.json
├── vite.config.ts                # 含 dev proxy
└── package.json
```

---

## 4. 页面布局

单页 Dashboard，无路由。

```
┌─────────────────────────────────────────────────────────────┐
│  Header: Rivus Agent | SSE状态点 | API状态点                │
├──────────────┬──────────────────────────────────────────────┤
│              │  RunForm（折叠卡片，activeRun 时自动收起）   │
│  SessionPanel│  ─────────────────────────────────────────   │
│  （240px）   │  RunDetail                                   │
│              │  ┌ StatusBadge + ActionBar ┐                 │
│  会话列表    │  ├ Steps Timeline          ┐                 │
│  新建按钮    │  ├ Verification 结果      ┐                 │
│              │  └ result_json 折叠卡    ┘                 │
│  历史 Run    ├──────────────────────────────────────────────│
│  列表（点击  │  EventStream（终端风格，可折叠）              │
│  切换）      │  过滤 chips + 搜索框                          │
│              │  事件按时间序展示，approval 内嵌 ApprovalCard  │
└──────────────┴──────────────────────────────────────────────┘
```

---

## 5. 数据流

### 5.1 会话切换

1. `SessionPanel` 渲染会话列表（调用 `GET /sessions/{id}`）
2. 用户点击 → `sessionStore.switchSession(id)` → 触发 `runStore.loadRunHistory(id)` 拉取该 session 下的历史 Run
3. 历史 Run 列表渲染在 SessionPanel 下方，点击某 Run → `runStore.activateRun(runId)` → 主区切换

### 5.2 任务提交

1. `RunForm` 提交 → `api.createRun(req, idempotencyKey)`
2. 成功 → `runStore.activateRun(runId)` + 开始 SSE 订阅（`useEventStream(runId)`)
3. 若返回 `duplicated: true` → toast 提示幂等命中，直接切换到已有 Run

### 5.3 SSE 事件流

- `useEventStream(runId)` hook：
  - 订阅时携带 `Last-Event-ID`（从 `runStore.events` 取最大 seq）
  - 收到 `AgentEvent` → `runStore.appendEvent(e)`
  - 连接状态变化 → `uiStore.setConnectionStatus(status)`
- 断连自动指数退避重连（1s/2s/4s，上限 30s），重连时携带最后 seq
- `runStore.events` 超出 500 条时截断（保留最新 500 条）

### 5.4 Run 状态同步

- SSE 连接正常时以事件流为主（`run.status` 通过 `run.finished/failed/cancelled` 事件更新）
- SSE 断连时每 3s 轮询 `GET /runs/{id}` 兜底，确保状态不卡住
- SSE 重连成功后停止轮询

### 5.5 审批操作

1. `EventStream` 渲染 `approval.requested` 事件 → 展开 `ApprovalCard`
2. 用户点击批准/拒绝 → `api.decideApproval(runId, approvalId, approve, reason)`
3. 成功 → 本地标记卡片为 `decided`，等待 `approval.decided` SSE 事件确认
4. 卡片显示最终决定结果和 reason

---

## 6. 类型定义

```typescript
// types.ts

type RunStatus =
  | "queued" | "running" | "waiting_approval" | "paused"
  | "succeeded" | "failed" | "cancelled" | "timed_out"

type StepStatus =
  | "pending" | "running" | "succeeded" | "failed"
  | "skipped" | "awaiting_approval"

type EventType =
  | "run.created" | "run.started" | "run.finished" | "run.failed"
  | "run.cancelled" | "run.resumed"
  | "plan.created" | "plan.updated"
  | "step.started" | "step.finished"
  | "model.requested" | "model.completed" | "model.failed"
  | "tool.requested" | "tool.authorized" | "tool.denied"
  | "tool.started" | "tool.completed" | "tool.failed"
  | "approval.requested" | "approval.decided"
  | "checkpoint.saved"
  | "verification.passed" | "verification.failed"

interface AgentEvent {
  seq: number
  run_id: string
  event_type: EventType
  payload_json: string   // 保留原文，组件内按需 JSON.parse
  sensitivity: string
  created_at: number     // Unix 毫秒
}

interface Run {
  id: string
  session_id: string
  status: RunStatus
  mode: string
  goal: string
  error_code?: string
  error_summary?: string
  result_json?: string
  created_at: number
  updated_at: number
}

interface Step {
  id: string
  run_id: string
  step_index: number
  description: string
  status: StepStatus
  result_summary?: string
}

interface Approval {
  id: string
  run_id: string
  tool_call_id: string
  tool_name: string
  args_hash: string
  status: string
  requested_by: string
  expires_at: number
}

interface Session {
  session_id: string
  title: string
  created_at: number
  updated_at: number
}

interface CreateRunReq {
  session_id: string
  goal: string
  constraints?: string[]
  success_criteria?: string[]
  mode?: "react" | "plan_execute"
  budget?: {
    max_duration_seconds?: number
    max_model_calls?: number
    max_tool_calls?: number
    max_iterations?: number
    max_output_bytes?: number
  }
}
```

---

## 7. API 客户端

```typescript
// api.ts

class ApiError extends Error {
  code: string
  retryable: boolean
  constructor(code: string, message: string, retryable: boolean) { ... }
}

class ApiClient {
  constructor(opts: { base: string; owner: string; token?: string })

  private headers(): Record<string, string>
  // 注入 X-Owner-ID、Authorization（若有 token）

  async createSession(title: string): Promise<{ session_id: string }>
  async getSession(id: string): Promise<Session>
  async createRun(req: CreateRunReq, idempotencyKey?: string):
    Promise<{ run_id: string; status: RunStatus; duplicated: boolean }>
  async getRun(id: string): Promise<{ run: Run; steps: Step[] }>
  async cancelRun(id: string): Promise<void>
  async resumeRun(id: string): Promise<void>
  async decideApproval(
    runId: string, approvalId: string,
    approve: boolean, reason?: string,
  ): Promise<void>
}
```

- 所有请求失败（非 2xx）抛出 `ApiError`
- `createRun` 自动注入 `Idempotency-Key` header（由调用方传入）

---

## 8. SSE 客户端

```typescript
// sse.ts

type SseStatus = "connected" | "reconnecting" | "closed"

function sseSubscribe(opts: {
  url: string          // 如 /api/v1/runs/{id}/events
  token?: string
  owner: string
  lastEventId?: number // 初始 seq，用于补发
  onEvent: (e: AgentEvent) => void
  onStatus: (s: SseStatus) => void
}): () => void         // 返回 unsubscribe
```

实现细节：
- 基于 `fetch` + `ReadableStream`，手动解析 SSE 帧（`id:` / `event:` / `data:` / 空行）
- 断连后指数退避重连（1s/2s/4s/8s/16s/30s，上限 30s）
- 重连请求携带 `Last-Event-ID` header（取本地最大 seq）
- 组件 unmount 时调用 unsubscribe，终止 fetch 和重连定时器

React hook 封装：

```typescript
// 在 store 层调用，避免 hook 嵌套问题
function useEventStream(runId: string | null): void
// 内部：runId 变化时重新订阅；unmount 时取消
```

---

## 9. Zustand Stores

### session-store

```typescript
{
  sessions: Session[]
  activeSessionId: string | null
  createSession(title: string): Promise<void>
  switchSession(id: string): void
  loadSessions(): Promise<void>
}
```

### run-store

```typescript
{
  activeRunId: string | null
  activeRun: Run | null
  activeSteps: Step[]
  events: AgentEvent[]          // 最多保留 500 条
  eventFilter: EventType | "all"
  runHistory: Run[]            // 当前 session 的历史 Run

  activateRun(id: string): Promise<void>
  appendEvent(e: AgentEvent): void
  setEventFilter(f: EventType | "all"): void
  clearEvents(): void
  pollRunStatus(): Promise<void>  // SSE 断连时的兜底轮询
}
```

### ui-store

```typescript
{
  eventStreamOpen: boolean
  sidebarCollapsed: boolean
  sseStatus: SseStatus
  apiReachable: boolean

  toggleEventStream(): void
  setSseStatus(s: SseStatus): void
  setApiReachable(b: boolean): void
}
```

---

## 10. 核心组件

### Header

- 左侧：`Rivus Agent` 标题
- 右侧：两个状态点（颜色：绿=正常，黄=重连中，红=断开）+ 文字说明 tooltip
- 高度 48px，`border-bottom`

### SessionPanel（左侧 240px）

- **会话区**：
  - 会话列表（title + 时间），点击切换
  - "新建会话" 按钮
- **历史 Run 区**（选中 session 后显示）：
  - 该 session 下最近 20 条 Run，显示 goal 截断 + 状态 badge
  - 点击切换 activeRun

### RunForm

- 主区上方卡片，`activeRun` 存在时自动收起（ChevronDown 展开）
- 必填：`goal`（textarea，2 行）
- 高级选项（折叠）：
  - `mode`：Segmented Control（ReAct / Plan-Execute）
  - `constraints`：动态输入列表（+ 添加，× 删除）
  - `success_criteria`：同上
  - `budget`：5 个数字输入框，预填服务端默认值
- 提交按钮：loading 态显示 spinner，成功后 toast

### RunDetail

- 顶部：`run_id`（可复制，lucide Copy 图标）+ `StatusBadge` + `ActionBar`
- `StatusBadge` 颜色映射：

  | 状态 | 颜色 |
  |---|---|
  | queued / paused / cancelled | 灰色 |
  | running | 蓝色 + 脉冲动画 |
  | waiting_approval | 黄色 |
  | succeeded | 绿色 |
  | failed | 红色 |
  | timed_out | 橙色 |

- `Steps Timeline`：垂直列表，每行 `step_index` + `description` + `StepBadge`；`running` 时显示转圈 icon
- `Verification`：`verification.passed` / `verification.failed` 事件后渲染，结果摘要卡片
- `result_json`：折叠卡片，展开显示格式化 JSON

### EventStream

- 终端风格：`bg-zinc-950 text-zinc-100` 深色背景，等宽字体（`font-mono`）
- 可折叠（ui-store.eventStreamOpen）
- 顶部工具条：
  - 过滤 chips：`all` / `run` / `model` / `tool` / `step` / `approval`
  - 搜索框（客户端过滤 `payload_json` 字符串，不区分大小写）
- 事件行：`HH:MM:SS` + `EventTypeBadge`（按类型着色）+ 摘要文本
  - `tool.started/completed`：显示工具名
  - `model.requested`：显示当前调用计数
  - `approval.requested`：展开 `ApprovalCard`
- 自动滚动：新事件到达时滚到底部；用户手动上滚后暂停自动滚动，新事件到达时显示"↓ 新事件"浮标

### ApprovalCard

- 嵌在 `approval.requested` 事件行下方
- 展示：`tool_name`（大字）+ 参数摘要（`args_hash` 对应的可读字段）+ 风险等级 badge + 过期时间倒计时（mm:ss）
- 操作按钮（未决定时）：
  - "批准"（绿色）→ 直接调用 `decideApproval(approve=true)`
  - "拒绝"（红色）→ 弹出 Dialog 输入 reason，提交
- 已决定（`decided`）：显示结果 badge + reason，按钮置灰
- 已过期（`expires_at < now`）：显示"已过期"，按钮置灰

### ActionBar

- 依据 `activeRun.status` 渲染：

  | 状态 | 显示按钮 |
  |---|---|
  | queued / running | "取消"（confirm dialog） |
  | paused | "恢复" |
  | waiting_approval | "查看审批"（滚动到 EventStream 中对应 ApprovalCard） |
  | succeeded / failed / cancelled / timed_out | "创建新 Run"（展开 RunForm） |

---

## 11. 开发体验

### Vite dev proxy

```ts
// vite.config.ts
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/api':    'http://localhost:8080',
      '/healthz':'http://localhost:8080',
      '/readyz': 'http://localhost:8080',
    }
  }
})
```

### 本地开发流程

```bash
# 终端 1：启动后端
cd backend && make run

# 终端 2：启动前端
cd frontend && pnpm dev
# → http://localhost:5173
```

### 生产构建

```bash
cd frontend && pnpm build
# 输出到 frontend/dist/，由后端 HTTP 服务或任意静态服务器托管
```

`.gitignore` 已含 `frontend/dist/` 和 `frontend/node_modules/`。

---

## 12. 后端补充需求

当前后端 `handleEvents` 是"一次性返回历史后结束"，不是真正持久的 SSE 流。

**短期（前端可独立工作）**：前端退化为"轮询 `GET /events?after=lastSeq` 每 2s"，UI 体验可接受。

**后续（后端改造）**：服务端维持长连接，新事件实时推送；前端 SSE 客户端无需改动，只切换 URL 和轮询兜底逻辑。

改造范围：
- `EventRepo` 增加 watch/poll 机制（内部 goroutine 轮询新 seq，通过 channel 推送）
- `handleEvents` 改为阻塞写入，连接关闭时退出
- 前端代码不变，`sseSubscribe` 的 `onStatus` 会自然反映长连接状态

---

## 13. 测试策略

| 层 | 工具 | 重点 |
|---|---|---|
| 组件单测 | Vitest + RTL | EventStream 渲染、ApprovalCard 操作流、ActionBar 状态映射、RunForm 提交 |
| API 客户端 | Vitest（mock fetch） | 各端点请求格式、错误处理、幂等 key 注入 |
| SSE 客户端 | Vitest（mock ReadableStream） | 断连重连、Last-Event-ID 补发、帧解析 |
| Store | Vitest（Zustand test utilities） | 状态转换、events 截断逻辑 |

不做 E2E（Playwright），留待后续。

---

## 14. 风险与缓解

| 风险 | 缓解 |
|---|---|
| 后端 SSE 非持久，前端只能轮询 | 前端封装层支持轮询兜底；后端改造后无缝切换 |
| `payload_json` 体积大，事件流渲染卡顿 | 默认截断到 500 条；`payload_json` 按需 parse（不在行渲染时全量 JSON.parse） |
| shadcn/ui 组件数量多，初次脚手架耗时 | 只安装实际用到的组件，按需添加 |
| Zustand store 耦合 | 三个 store 职责清晰；跨 store 操作通过 action 组合，不在组件层直接 mutate 其他 store |
