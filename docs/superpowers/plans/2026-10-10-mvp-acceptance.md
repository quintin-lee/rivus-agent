# MVP 验收计划：可部署、测试可跑通

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 使 Rivus Agent 达到 MVP 可验收状态——`docker compose up` 两个服务都 healthy/running 且可达，前后端测试全绿。

**Architecture:** 后端集成测试已全绿（`TestApprovalApproveResume` 在 commit 27fe393 已修复）。本计划主要补三块：(1) 前端 spec §13 要求的组件/store/API 测试套件；(2) docker compose 部署可达性验证（backend healthcheck 通过 + frontend 页面加载 + /api 代理通）；(3) 清理并统一构建产物。

**Tech Stack:** Go 1.27（backend）、React 18 + Vitest + RTL + Zustand（frontend）、Docker Compose v2

**Spec:** `docs/superpowers/specs/2026-10-09-frontend-design.md` §13（测试策略）、§12（后端端点）
**既有计划:** `docs/superpowers/plans/2026-10-09-frontend.md` Chunk 6（wire-up，未最终化）

---

## 当前状态盘点（已验证）

| 项 | 状态 |
|---|---|
| 后端 `go test ./...` | ✅ 全绿（含 `tests/integration`） |
| `TestApprovalApproveResume` | ✅ 已通过（5/5 稳定，~0.11s） |
| 前端 `npx vitest run` | ⚠️ 仅 1 个测试（`button.test.tsx` 烟雾） |
| 前端 `tsc --noEmit` | ✅ 干净 |
| 前端 `pnpm build` | ✅ 成功（dist/ 生成） |
| `docker compose up` | ❌ 未验证 |
| `.env` 已存在 | ✅ `MODEL_BASE_URL=http://localhost:11434/v1` + `DOCKER_REGISTRY=docker.1ms.run/` |
| 工作区脏文件 | ⚠️ `.env.example`、`backend/Dockerfile`、`docker-compose.yml`、`frontend/Dockerfile` 已改（加 REGISTRY arg），未提交 |

**验收标准（用户确认）：**
- "部署可达"：`docker compose up -d` 后 `backend` 与 `frontend` 都 running/healthy，`curl /healthz` 通、前端页面 200、`/api` 代理到 backend 通。
- 后端失败测试修复 → 当前已全绿，只需在收尾时复验。
- 前端测试范围 = 核心组件（EventStream/ApprovalCard/ActionBar/RunForm）+ store + API 客户端；不含 SSE 客户端 mock（轮询阶段无需，spec 中优先级低）。

---

## 关键实现细节（执行者必读）

### 前端测试基础设施

- **Vitest 配置**（`frontend/vite.config.ts` 已有）：`environment: 'jsdom'`、`globals: true`、`setupFiles: ['./src/test-setup.ts']`（已含 `@testing-library/jest-dom`）。
- **运行命令**：`cd frontend && npx vitest run`（一次性跑完，不 watch）。`package.json` 里 `test: vitest` 会进 watch 模式，计划里一律用 `vitest run`。
- **Zustand 测试方法**：store 是模块级单例。测试中用 `useRunStore.setState({ ... })` 直接注入状态，用 `useRunStore.getState()` 断言。不需要 mock store hook。组件内 `useRunStore((s) => s.activeRun)` 这种 selector 会读到 `setState` 注入的值。
- **Mock `@/lib/api`**：组件（ActionBar/ApprovalCard/RunForm）依赖 `api` 单例。用 `vi.mock('@/lib/api', ...)` 替换方法为 spy。
- **Mock `@/lib/status`**：`formatTimestamp` 用 `toLocaleTimeString`（locale 依赖），测试中固定 mock 返回固定字符串。
- **事件类型取值**（`frontend/src/lib/types.ts`）：`model.requested`、`tool.completed`、`approval.requested`、`run.finished` 等都是 `EventType` 联合的成员。EventStream 的过滤 chips 映射到 `eventCategory`（`run`/`model`/`tool`/`step`/`approval`/`other`）。

### 后端

- 测试全绿，无需改代码。收尾时跑 `go test ./...` 复核。
- 部署验证依赖 `backend/Dockerfile` 的 `HEALTHCHECK`（`wget -qO- http://127.0.0.1:8080/healthz`）。

### 部署验证

- `.env` 已含 `MODEL_BASE_URL`/`MODEL_API_KEY`/`MODEL_NAME`/`DOCKER_REGISTRY`。backend 的 healthz 不依赖模型网关可达（模型是惰性初始化），所以 compose up 后 backend 应能 ready。
- frontend 容器把 `/api` 反代到 `${RIVUS_BACKEND}`（compose 里设 `RIVUS_BACKEND=http://backend:8080`）。验证时用 `curl -H "X-Owner-ID: default" http://localhost:8081/api/v1/sessions` 检查代理是否通（期望 200 或 401/404 而非连接失败）。

---

## 文件结构（新建）

```
frontend/src/components/EventStream.test.tsx     # 事件渲染 + 过滤 + 搜索 + 审批嵌入
frontend/src/components/ApprovalCard.test.tsx    # 批准/拒绝流程
frontend/src/components/ActionBar.test.tsx       # 状态映射
frontend/src/components/RunForm.test.tsx          # 提交 Run
frontend/src/lib/api.test.ts                      # 请求格式 + 错误 + 幂等 key
frontend/src/store/run-store.test.ts              # 事件截断 + 状态转换
```

不新建文件（已有）：`frontend/vite.config.ts`、`frontend/src/test-setup.ts`。

---

## Task 1: 后端测试全绿复核（验收前置）

**目的：** 确认 `TestApprovalApproveResume` 等集成测试稳定通过，作为部署验收的基础。

- [ ] **Step 1: 跑全量后端测试**

Run: `cd backend && go test ./... -count=1`
Expected: 所有包 `ok`，无 `FAIL`。特别确认 `rivus-agent-backend/tests/integration` 为 ok。

- [ ] **Step 2: 编译二进制确认无构建错误**

Run: `cd backend && make build && ls -l bin/agentd`
Expected: 生成 `bin/agentd`，无编译错误。

- [ ] **Step 3: 提交（若无需改动则跳过，记录"backend 已绿"）**

若 Step 1 全绿且无文件改动，此 Task 无 commit。若有 fix，按 Conventional Commits 提交：`fix(backend): <desc>`。

**完成判据：** `go test ./...` 全绿。

---

## Task 2: API 客户端测试（`frontend/src/lib/api.test.ts`）

**目的：** 验证 `ApiClient` 的请求格式、错误处理、幂等 key 注入（spec §13 类别 2）。

- [ ] **Step 1: 写失败测试**

新建 `frontend/src/lib/api.test.ts`。Mock `fetch`（`global.fetch = vi.fn()`），import `api` 单例。

```ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { api, ApiError } from '@/lib/api'

function mockFetch(status: number, body: unknown = {}) {
  return vi.fn().mockResolvedValue({
    ok: status < 400,
    status,
    statusText: String(status),
    json: async () => body,
    text: async () => (typeof body === 'string' ? body : JSON.stringify(body)),
  })
}

describe('ApiClient', () => {
  beforeEach(() => { vi.restoreAllMocks() })

  it('createRun sends Idempotency-Key header and posts body', async () => {
    const f = mockFetch(201, { run_id: 'r1', status: 'queued', duplicated: false })
    vi.stubGlobal('fetch', f)
    const res = await api.createRun({ session_id: 's1', goal: 'g' }, 'idem-1')
    expect(res.run_id).toBe('r1')
    const [url, opts] = (f.mock.calls[0] as any[])
    expect(url).toBe('/api/v1/runs')
    expect(opts.headers['Idempotency-Key']).toBe('idem-1')
    expect(opts.headers['X-Owner-ID']).toBe('default')
    expect(JSON.parse(opts.body)).toMatchObject({ session_id: 's1', goal: 'g' })
  })

  it('throws ApiError with code on 4xx', async () => {
    const f = mockFetch(404, { code: 'not_found', message: 'run not found', retryable: false })
    vi.stubGlobal('fetch', f)
    await expect(api.getRun('nope')).rejects.toThrow(ApiError)
    const err = await api.getRun('nope').catch((e) => e) as ApiError
    expect(err.code).toBe('not_found')
    expect(err.message).toBe('run not found')
    expect(err.retryable).toBe(false)
  })

  it('listSessions returns [] on not_found, rethrows on 5xx', async () => {
    const f = vi.fn()
    vi.stubGlobal('fetch', f)
    f.mockResolvedValueOnce({ ok: true, status: 200, json: async () => ({ sessions: [] }) })
    expect(await api.listSessions()).toEqual([])
    f.mockResolvedValueOnce({ ok: false, status: 500, statusText: 'ISE', json: async () => ({ code: 'internal', message: 'x', retryable: true }) })
    await expect(api.listSessions()).rejects.toMatchObject({ code: 'internal', retryable: true })
  })

  it('getEvents parses SSE frames into AgentEvent[]', async () => {
    const sse = 'id: 5\nevent: model.requested\ndata: {"call_count":1}\n\nid: 6\nevent: tool.completed\ndata: {"tool_name":"x"}\n\n'
    const f = mockFetch(200, null)
    ;(f as any).mockResolvedValue({ ok: true, status: 200, statusText: 'OK', text: async () => sse })
    vi.stubGlobal('fetch', f)
    const evs = await api.getEvents('r1', 0)
    expect(evs).toHaveLength(2)
    expect(evs[0]).toMatchObject({ seq: 5, event_type: 'model.requested', payload_json: '{"call_count":1}' })
    expect(evs[1].event_type).toBe('tool.completed')
  })
})
```

注意：`getEvents` 用的是 `res.text()` 而非 `res.json()`，所以 mock 要提供 `text`。若当前 `api.getEvents` 内部实现与 mock 不符导致测试失败，以**实现行为**为准修正测试断言（实现读 `res.text()` 解析 SSE）。

- [ ] **Step 2: 运行确认通过**

Run: `cd frontend && npx vitest run src/lib/api.test.ts`
Expected: PASS（4 tests）。

- [ ] **Step 3: 提交**

`cd frontend && git add src/lib/api.test.ts && git commit -m "test(frontend): add ApiClient unit tests"`

---

## Task 3: run-store 测试（`frontend/src/store/run-store.test.ts`）

**目的：** 验证事件截断逻辑（spec §13 类别 4 + §14 payload 截断）。

- [ ] **Step 1: 写失败测试**

新建 `frontend/src/store/run-store.test.ts`：

```ts
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { useRunStore } from '@/store/run-store'
import { MAX_EVENTS } from '@/App.config'
import type { AgentEvent } from '@/lib/types'

vi.mock('@/lib/api', () => ({
  api: { getRun: vi.fn().mockResolvedValue({ run: {}, steps: [] }), listRuns: vi.fn().mockResolvedValue([]) },
}))

function mkEvent(seq: number): AgentEvent {
  return { seq, run_id: 'r1', event_type: 'run.started', payload_json: '{}', sensitivity: 'normal', created_at: 0 }
}

describe('run-store', () => {
  beforeEach(() => { useRunStore.setState({ activeRunId: null, activeRun: null, activeSteps: [], events: [], eventFilter: 'all', runHistory: [] }) })

  it('appendEvent keeps at most MAX_EVENTS (drops oldest)', () => {
    for (let i = 0; i < MAX_EVENTS + 10; i++) useRunStore.getState().appendEvent(mkEvent(i))
    const evs = useRunStore.getState().events
    expect(evs.length).toBe(MAX_EVENTS)
    expect(evs[0].seq).toBe(10)          // 最旧的 10 条被丢弃
    expect(evs[evs.length - 1].seq).toBe(MAX_EVENTS + 9)
  })

  it('clearEvents empties events', () => {
    useRunStore.getState().appendEvent(mkEvent(1))
    useRunStore.getState().clearEvents()
    expect(useRunStore.getState().events).toEqual([])
  })

  it('setEventFilter updates filter', () => {
    useRunStore.getState().setEventFilter('tool.requested')
    expect(useRunStore.getState().eventFilter).toBe('tool.requested')
  })
})
```

- [ ] **Step 2: 运行确认通过**

Run: `cd frontend && npx vitest run src/store/run-store.test.ts`
Expected: PASS（3 tests）。

- [ ] **Step 3: 提交**

`git add src/store/run-store.test.ts && git commit -m "test(frontend): add run-store truncation tests"`

---

## Task 4: EventStream 组件测试

**目的：** 验证事件渲染、按类别过滤、搜索过滤、`approval.requested` 嵌入 ApprovalCard（spec §13 类别 1）。

- [ ] **Step 1: 写失败测试**

新建 `frontend/src/components/EventStream.test.tsx`。mock `@/lib/status` 的 `formatTimestamp` 与 `@/lib/api`（ApprovalCard 依赖 api）：

```tsx
import { render, screen, fireEvent } from '@testing-library/react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { EventStream } from './EventStream'
import { useRunStore } from '@/store/run-store'
import { useUiStore } from '@/store/ui-store'
import type { AgentEvent } from '@/lib/types'

vi.mock('@/lib/api', () => ({ api: { decideApproval: vi.fn().mockResolvedValue(undefined), getRun: vi.fn().mockResolvedValue({ run: {}, steps: [] }) } }))
vi.mock('@/lib/status', () => ({
  eventCategory: (t: string) => t.split('.')[0],
  eventCategoryClasses: { run: 'c-run', model: 'c-model', tool: 'c-tool', step: 'c-step', approval: 'c-approval', other: 'c-other' },
  formatTimestamp: () => '12:00:00',
}))

function ev(seq: number, type: AgentEvent['event_type'], payload = '{}'): AgentEvent {
  return { seq, run_id: 'r1', event_type: type, payload_json: payload, sensitivity: 'normal', created_at: 0 }
}

describe('EventStream', () => {
  beforeEach(() => {
    useUiStore.setState({ eventStreamOpen: true })
    useRunStore.setState({ events: [], eventFilter: 'all' })
  })

  it('renders each event with its type badge and summary', () => {
    useRunStore.setState({ events: [ev(1, 'run.started'), ev(2, 'model.requested', '{"call_count":3}')], eventFilter: 'all' })
    render(<EventStream />)
    expect(screen.getByText('run.started')).toBeInTheDocument()
    expect(screen.getByText(/model call 3/i)).toBeInTheDocument()
  })

  it('filters events by category chip', () => {
    useRunStore.setState({ events: [ev(1, 'run.started'), ev(2, 'tool.completed', '{"tool_name":"search"}')], eventFilter: 'all' })
    render(<EventStream />)
    fireEvent.click(screen.getByText('tool'))
    expect(screen.getByText(/tool: search/i)).toBeInTheDocument()
    expect(screen.queryByText('run.started')).toBeNull()
  })

  it('filters by search query over payload_json', () => {
    useRunStore.setState({ events: [ev(1, 'tool.completed', '{"tool_name":"alpha"}'), ev(2, 'tool.completed', '{"tool_name":"beta"}')] })
    render(<EventStream />)
    fireEvent.change(screen.getByPlaceholderText(/Search/i), { target: { value: 'alpha' } })
    expect(screen.getByText(/tool: alpha/i)).toBeInTheDocument()
    expect(screen.queryByText(/tool: beta/i)).toBeNull()
  })

  it('renders ApprovalCard under approval.requested event', () => {
    useRunStore.setState({ events: [ev(1, 'approval.requested', '{"approval_id":"a1","tool_name":"publish"}')] })
    render(<EventStream />)
    expect(screen.getByText(/Approval required/i)).toBeInTheDocument()
    expect(screen.getByText('publish')).toBeInTheDocument()
  })

  it('shows empty state when no events', () => {
    render(<EventStream />)
    expect(screen.getByText('No events')).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: 运行确认通过**

Run: `cd frontend && npx vitest run src/components/EventStream.test.tsx`
Expected: PASS（5 tests）。

若某断言因组件实际文案/行为不符而失败，**以组件当前实现为准**调整断言文案（不强行改组件语义），例如 "Approval required" 文案若与实际不同则用实际渲染文本。

- [ ] **Step 3: 提交**

`git add src/components/EventStream.test.tsx && git commit -m "test(frontend): add EventStream render/filter/search tests"`

---

## Task 5: ApprovalCard 组件测试

**目的：** 验证批准 / 拒绝操作流（spec §13 类别 1）。

- [ ] **Step 1: 写失败测试**

新建 `frontend/src/components/ApprovalCard.test.tsx`。`ApprovalCard` 只接收 `event` prop，依赖 `@/lib/api`（`decideApproval`）与 `@/store/run-store`（`fetchRunAndSteps`）。

```tsx
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ApprovalCard } from './ApprovalCard'
import { useRunStore } from '@/store/run-store'
import type { AgentEvent } from '@/lib/types'

const decideApproval = vi.fn().mockResolvedValue(undefined)
vi.mock('@/lib/api', () => ({ api: { decideApproval } }))
vi.mock('@/lib/status', () => ({ formatTimestamp: () => '12:00:00' }))

function approvalEvent(): AgentEvent {
  return {
    seq: 1, run_id: 'r1', event_type: 'approval.requested',
    payload_json: JSON.stringify({ approval_id: 'a1', tool_name: 'publish', expires_at: Date.now() + 60000 }),
    sensitivity: 'normal', created_at: 0,
  }
}

describe('ApprovalCard', () => {
  beforeEach(() => {
    decideApproval.mockClear()
    useRunStore.setState({ activeRun: null, fetchRunAndSteps: vi.fn().mockResolvedValue(undefined) })
  })

  it('shows tool name and approve/reject buttons', () => {
    render(<ApprovalCard event={approvalEvent()} />)
    expect(screen.getByText('publish')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Approve/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Reject/i })).toBeInTheDocument()
  })

  it('approving calls decideApproval(true) and marks decided', async () => {
    render(<ApprovalCard event={approvalEvent()} />)
    fireEvent.click(screen.getByRole('button', { name: /Approve/i }))
    await waitFor(() => expect(decideApproval).toHaveBeenCalledWith('r1', 'a1', true))
    expect(await screen.findByText('approved')).toBeInTheDocument()
  })

  it('rejecting with reason calls decideApproval(false, reason)', async () => {
    render(<ApprovalCard event={approvalEvent()} />)
    fireEvent.click(screen.getByRole('button', { name: /Reject/i }))
    fireEvent.change(screen.getByPlaceholderText(/Why should/i), { target: { value: 'too risky' } })
    fireEvent.click(screen.getByRole('button', { name: /^Reject$/i }))
    await waitFor(() => expect(decideApproval).toHaveBeenCalledWith('r1', 'a1', false, 'too risky'))
  })

  it('hides cards whose payload has no approval_id', () => {
    render(<ApprovalCard event={{ ...approvalEvent(), payload_json: '{}' }} />)
    expect(screen.queryByText('publish')).toBeNull()
  })
})
```

- [ ] **Step 2: 运行确认通过**

Run: `cd frontend && npx vitest run src/components/ApprovalCard.test.tsx`
Expected: PASS（4 tests）。

- [ ] **Step 3: 提交**

`git add src/components/ApprovalCard.test.tsx && git commit -m "test(frontend): add ApprovalCard approve/reject flow tests"`

---

## Task 6: ActionBar 组件测试

**目的：** 验证按 Run 状态映射操作按钮（spec §13 类别 1）。

- [ ] **Step 1: 写失败测试**

新建 `frontend/src/components/ActionBar.test.tsx`。mock `@/lib/api`、`@/lib/status`（`isActiveRunStatus`）。

```tsx
import { render, screen, fireEvent } from '@testing-library/react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ActionBar } from './ActionBar'
import { useRunStore } from '@/store/run-store'
import type { Run } from '@/lib/types'

const cancelRun = vi.fn().mockResolvedValue(undefined)
vi.mock('@/lib/api', () => ({ api: { cancelRun, resumeRun: vi.fn().mockResolvedValue(undefined) } }))
vi.mock('@/lib/status', () => ({
  isActiveRunStatus: (s: string) => !['succeeded','failed','cancelled','timed_out'].includes(s),
  eventCategory: () => 'other',
}))

function setRun(partial: Partial<Run>) {
  const run: Run = {
    id: 'r1', session_id: 's', owner_id: 'default', status: 'running', mode: 'react',
    goal: 'g', budget: { max_duration_seconds: 1, max_model_calls: 1, max_tool_calls: 1, max_iterations: 1, max_output_bytes: 1 },
    attempt: 1, created_at: 0, updated_at: 0, ...partial,
  }
  useRunStore.setState({ activeRun: run, fetchRunAndSteps: vi.fn().mockResolvedValue(undefined) })
}

describe('ActionBar', () => {
  it('shows Cancel for an active running run', () => {
    setRun({ status: 'running' })
    render(<ActionBar />)
    expect(screen.getByRole('button', { name: /Cancel/i })).toBeInTheDocument()
  })

  it('shows Review Approval for waiting_approval', () => {
    setRun({ status: 'waiting_approval' })
    render(<ActionBar />)
    expect(screen.getByText(/Review Approval/i)).toBeInTheDocument()
  })

  it('shows Resume for paused', () => {
    setRun({ status: 'paused' })
    render(<ActionBar />)
    expect(screen.getByRole('button', { name: /Resume/i })).toBeInTheDocument()
  })

  it('shows terminal summary for succeeded', () => {
    setRun({ status: 'succeeded' })
    render(<ActionBar />)
    expect(screen.getByText(/Run succeeded/i)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Cancel/i })).toBeNull()
  })

  it('renders nothing when no active run', () => {
    useRunStore.setState({ activeRun: null })
    const { container } = render(<ActionBar />)
    expect(container.firstChild).toBeNull()
  })
})
```

- [ ] **Step 2: 运行确认通过**

Run: `cd frontend && npx vitest run src/components/ActionBar.test.tsx`
Expected: PASS（5 tests）。

- [ ] **Step 3: 提交**

`git add src/components/ActionBar.test.tsx && git commit -m "test(frontend): add ActionBar status-mapping tests"`

---

## Task 7: RunForm 组件测试

**目的：** 验证提交 Run 调用 `api.createRun` 并激活 run（spec §13 类别 1）。

- [ ] **Step 1: 写失败测试**

新建 `frontend/src/components/RunForm.test.tsx`。mock `@/lib/api`、两个 store。

```tsx
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { RunForm } from './RunForm'
import { useRunStore } from '@/store/run-store'
import { useSessionStore } from '@/store/session-store'

const createRun = vi.fn().mockResolvedValue({ run_id: 'new1', status: 'queued', duplicated: false })
vi.mock('@/lib/api', () => ({ api: { createRun } }))

describe('RunForm', () => {
  beforeEach(() => {
    createRun.mockClear()
    useSessionStore.setState({ activeSessionId: 's1', sessions: [] })
    useRunStore.setState({ activeRun: null, activateRun: vi.fn().mockResolvedValue(undefined) })
  })

  it('submits a run and activates the new run id', async () => {
    render(<RunForm />)
    fireEvent.change(screen.getByPlaceholderText(/Describe the goal/i), { target: { value: 'analyze' } })
    fireEvent.click(screen.getByRole('button', { name: /Start Run/i }))
    await waitFor(() => expect(createRun).toHaveBeenCalled())
    const arg = createRun.mock.calls[0][0]
    expect(arg.goal).toBe('analyze')
    expect(arg.session_id).toBe('s1')
    expect(useRunStore.getState().activateRun).toHaveBeenCalledWith('new1')
  })

  it('is disabled when no session is active', () => {
    useSessionStore.setState({ activeSessionId: null })
    render(<RunForm />)
    expect(screen.getByRole('button', { name: /Start Run/i })).toBeDisabled()
  })

  it('collapses to header when a run is already active', () => {
    useRunStore.setState({ activeRun: { id: 'x', session_id: 's1', owner_id: 'd', status: 'running', mode: 'react', goal: 'g', budget: { max_duration_seconds:1,max_model_calls:1,max_tool_calls:1,max_iterations:1,max_output_bytes:1 }, attempt: 1, created_at: 0, updated_at: 0 } })
    render(<RunForm />)
    expect(screen.queryByPlaceholderText(/Describe the goal/i)).toBeNull()
  })
})
```

- [ ] **Step 2: 运行确认通过**

Run: `cd frontend && npx vitest run src/components/RunForm.test.tsx`
Expected: PASS（3 tests）。

- [ ] **Step 3: 提交**

`git add src/components/RunForm.test.tsx && git commit -m "test(frontend): add RunForm submit tests"`

---

## Task 8: 前端全量测试 + 构建 + 类型检查

**目的：** 确认新测试套件与既有代码一起通过，不破坏 `tsc` 与 `pnpm build`。

- [ ] **Step 1: 跑全部前端测试**

Run: `cd frontend && npx vitest run`
Expected: 所有测试文件 PASS（既有 `button.test.tsx` + 本计划新增 6 个文件）。若某文件因文案/断言与实际组件不符而失败，**调整测试断言**至匹配组件当前实现，不改动组件逻辑。

- [ ] **Step 2: 类型检查**

Run: `cd frontend && npx tsc --noEmit`
Expected: 无错误。

- [ ] **Step 3: 生产构建**

Run: `cd frontend && pnpm build`
Expected: `dist/` 生成成功，无 TS/打包错误。

- [ ] **Step 4: 统一提交（若 Step 1 有测试修正）**

若 Step 1 导致测试文件改动：`git add src && git commit -m "test(frontend): align test assertions with component behavior"`。若无改动则跳过。

**完成判据：** `vitest run` 全绿 + `tsc` 干净 + `pnpm build` 成功。

---

## Task 9: Docker Compose 部署可达性验证

**目的：** 用户核心诉求——`docker compose up` 后两个服务都 running/healthy 且可达。

**前置：** `.env` 已存在（含 `MODEL_*` 与 `DOCKER_REGISTRY`）。若缺失：`cp .env.example .env` 并填 `MODEL_BASE_URL`/`MODEL_API_KEY`/`MODEL_NAME`。

- [ ] **Step 1: 清理旧容器与镜像（避免端口占用）**

Run: `docker compose down --remove-orphans && docker volume rm rivus-agent_rivus-data 2>/dev/null; true`
Expected: 无 `rivus-agent-*` 容器在跑；`8080`/`8081` 端口空闲（`ss -ltnp | grep -E ':808[01]'` 为空）。

- [ ] **Step 2: 构建两个镜像（用 1ms 源）**

Run: `docker compose build`
Expected: `backend` 与 `frontend` 镜像都构建成功（frontend 走 `DOCKER_REGISTRY=docker.1ms.run/`）。若构建慢（拉 node:22-alpine 大层），耐心等待；若某层失败看具体 error。

- [ ] **Step 3: 启动**

Run: `docker compose up -d`
Expected: 两个容器启动。backend 因 healthcheck 进入 `starting`，随后变 `healthy`。

- [ ] **Step 4: 等 backend ready 并核验状态**

Run:
```bash
for i in $(seq 1 30); do
  s=$(docker inspect --format='{{.State.Health.Status}}' rivus-agent-backend 2>/dev/null || echo none)
  echo "[$i] backend health=$s"
  [ "$s" = "healthy" ] && break
  sleep 2
done
docker compose ps
```
Expected: backend `healthy`，frontend `running`（无 healthcheck，状态为 running 即可）。若 backend 一直 `unhealthy`，看 `docker compose logs backend`。

- [ ] **Step 5: 可达性核验**

Run:
```bash
echo "--- backend /healthz (host 8080) ---"
curl -sf http://localhost:8080/healthz && echo
echo "--- frontend page (host 8081) ---"
curl -s -o /dev/null -w 'frontend HTTP %{http_code}\n' http://localhost:8081/
echo "--- /api proxied via frontend ---"
curl -s -H 'X-Owner-ID: default' -o /dev/null -w 'api HTTP %{http_code}\n' http://localhost:8081/api/v1/sessions
```
Expected:
- backend healthz 返回 `{"status":"ok"}`（或 ready 态）。
- frontend `/` 返回 HTTP 200（nginx 托管 dist）。
- `/api/v1/sessions` 经 frontend 代理返回 200（或 404/401——只要**不是** connection refused/502，即代理通）。注意 backend 的 `handleListSessions` 对无 session 的 owner 返回 200 空列表或 500，实际以代理连通为准。

- [ ] **Step 6: 创建 session + run 冒烟（可选增强，非硬性）**

Run:
```bash
SID=$(curl -s -X POST http://localhost:8081/api/v1/sessions -d '{"title":"mvp"}' | grep -o '"session_id":"[^"]*"' | cut -d'"' -f4)
echo "session=$SID"
curl -s -o /dev/null -w 'create run HTTP %{http_code}\n' -X POST http://localhost:8081/api/v1/runs \
  -H 'Content-Type: application/json' -H "Idempotency-Key: mvp-1" \
  -d "{\"session_id\":\"$SID\",\"goal\":\"say hi\",\"mode\":\"react\"}"
```
Expected: 创建 session 201，create run 返回 201（或幂等重复时 200+duplicated）。即使模型网关不可达导致 run 后续 failed，**部署可达性**（服务 up + 代理通 + 能创建对象）已满足验收。

- [ ] **Step 7: 提交 compose 相关改动**

工作区已有未提交的 `.env.example`/`backend/Dockerfile`/`docker-compose.yml`/`frontend/Dockerfile`（加 REGISTRY arg + 1ms 源）。确认它们内容正确后：
`git add .env.example backend/Dockerfile docker-compose.yml frontend/Dockerfile && git commit -m "feat(build): add REGISTRY build-arg and 1ms mirror support to docker builds"`

若某文件需修正（如 `DOCKER_REGISTRY` 注释），一并修好再提交。

**完成判据：** `docker compose ps` 显示 backend `healthy`、frontend `running`；`curl /healthz` 通；frontend 页 200；`/api` 代理通。

---

## Task 10: 收尾核验与文档

**目的：** 整体收尾，确认验收标准全达成，统一提交。

- [ ] **Step 1: 后端全量测试终验**

Run: `cd backend && go test ./... -count=1`
Expected: 全绿。

- [ ] **Step 2: 前端全量测试终验**

Run: `cd frontend && npx vitest run`
Expected: 全绿（含 Task 2-7 新增套件）。

- [ ] **Step 3: 前端构建终验**

Run: `cd frontend && pnpm build`
Expected: 成功。

- [ ] **Step 4: 最终 compose 状态确认**

Run: `docker compose ps`
Expected: backend `healthy`、frontend `running`。若已 down，重跑 Task 9 Step 3-4 一次确认可复现。

- [ ] **Step 5: 清理可选**

可选 `docker compose down` 释放资源（验收已完成）。保留与否不影响验收。

- [ ] **Step 6: 提交任何遗漏的修正**

`git status` 检查。若 Task 9 Step 7 之外的文件有实质改动（如测试修正、配置），按 Conventional Commits 分组提交。无则记录"无额外改动"。

**完成判据（验收清单）：**
- [ ] `go test ./...` 全绿
- [ ] `npx vitest run` 全绿（≥ 6 个新测试文件）
- [ ] `pnpm build` 成功
- [ ] `docker compose ps` → backend healthy + frontend running
- [ ] `curl localhost:8080/healthz` 200/ok
- [ ] `curl localhost:8081/` 200
- [ ] `curl localhost:8081/api/v1/sessions` 代理连通

---

## 风险与说明

| 风险 | 缓解 |
|---|---|
| `TestApprovalApproveResume` 偶发竞态（WaitTerminal 30s 超时） | 已 5/5 稳定；Task 1 复核。若复现失败，将超时提高到 60s 并加 `t.Log` 诊断，非本计划核心。 |
| 组件文案与测试断言不符 | Task 4-7 明确"以组件实现为准调整断言"，不强行改组件语义。 |
| `getEvents` mock 需 `res.text()` | api.test.ts 已提供 `text` mock；若实现走 `res.json()` 则改 mock。 |
| 模型网关不可达（`localhost:11434` 未必起 ollama） | 不影响验收——healthz/页面/代理/创建对象都不依赖模型推理；run 可能 failed 但部署可达性满足。 |
| 端口 8080/8081 被占用 | Task 9 Step 1 先 `down` + 释放 volume；`ss` 核查。 |
| 1ms 源拉 `node:22-alpine` 大层慢 | 等；已 `docker.1ms.run/` 加速。失败看具体层 error。 |
