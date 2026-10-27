# 前端体验补全 + 工具载荷增强 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:executing-plans to implement this plan（本项目无 subagent，用户已约束）。Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 批A 记忆面板 + 批B 子 agent 执行树（纯前端）+ 批C 工具载荷增强（后端 tool.completed 事件增 args/output 摘要，前端 ToolCallsCard 富渲染）。

**Architecture:** 前端零后端依赖地补两块 UI（既有 REST 端点）；批C 用「stash + OnEvent 合并」最小侵入：gatewayTool 执行成功时记录 (args,output)（按 runID|toolName 隔离），drain 发 tool.completed 时取走塞进 ev.Payload，OnEvent 持久化时合并 {"text","tool"} 与 extra——事件次数不变、现有事件字节不变。

**Tech Stack:** React 18 + Vite + TS + Tailwind + Zustand + vitest/@testing-library/react；Go 1.27 + Eino ADK。

**Spec:** docs/superpowers/specs/2026-10-26-frontend-completion-design.md（d34db6c）

---

## 已验证事实（执行者免复验）

- `GET /api/v1/sessions/{id}/memories` → `{"memories":[Memory...]}`，limit 1..200 缺省 200，404=会话不存在。
- `domain.Memory` json：`id/session_id/owner_id/content/source_run_id(omitempty)/created_at`。
- `domain.Run` 已暴露 `parent_run_id`（前端 types.ts 尚无此字段）。
- `tool.completed` 唯一发射点 runner_eino.go drain（现 L319，payload `""`）；gatewayTool 构造点唯一（runner_child.go buildAgentFiltered L97-99）；InvokableRun 两条成功路径 L188/L200。
- runtime.go OnEvent（L42-48）`json.Marshal(map[string]string{"text","tool"})`，忽略 ev.Payload；全部 emit 调用点 payload 均为 `""`。
- 前端：`eventPayload(e)`（status.ts:120）安全解析 payload_json；`runStatusClasses`；`truncate(s,n)`；api.ts `ApiClient.request`；SessionList 会话行 hover 按钮模式（L153-159）；测试模式 = vitest + @testing-library/react + `vi.mock('@/lib/api')`（参考 ApprovalCard.test.tsx）。
- 批3 教训：并行子 agent 共享 EinoRunner → 观测数据必须按 runID 隔离。

---

## Chunk 1: 批A 记忆面板

### Task 1: types/api/MemoriesDialog/SessionList

**Files:**
- Modify: `frontend/src/lib/types.ts`（末尾 PendingGoal 前加 Memory）
- Modify: `frontend/src/lib/api.ts`（listRuns 后加 listMemories；import type 加 Memory）
- Create: `frontend/src/components/MemoriesDialog.tsx`
- Modify: `frontend/src/components/SessionList.tsx`
- Test: `frontend/src/components/MemoriesDialog.test.tsx`（新建）、`frontend/src/components/SessionList.test.tsx`（增补）

- [ ] **Step 1.1: 写失败测试** `frontend/src/components/MemoriesDialog.test.tsx`

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MemoriesDialog } from '@/components/MemoriesDialog'
import { api } from '@/lib/api'
import type { Memory } from '@/lib/types'

vi.mock('@/lib/api', () => ({ api: { listMemories: vi.fn() } }))

function mem(content: string, i = 0): Memory {
  return { id: `m${i}`, session_id: 's1', owner_id: 'o1', content, source_run_id: `run-${i}abcdef0`, created_at: 1700000000000 + i }
}

describe('MemoriesDialog', () => {
  beforeEach(() => vi.mocked(api.listMemories).mockReset())

  it('lists memories newest first with content', async () => {
    vi.mocked(api.listMemories).mockResolvedValue([mem('喜欢 Go', 1), mem('项目用 SQLite', 0)])
    render(<MemoriesDialog sessionId="s1" sessionTitle="t" open onOpenChange={() => {}} />)
    await waitFor(() => expect(screen.getByText(/喜欢 Go/)).toBeTruthy())
    expect(screen.getByText(/项目用 SQLite/)).toBeTruthy()
  })

  it('shows empty state', async () => {
    vi.mocked(api.listMemories).mockResolvedValue([])
    render(<MemoriesDialog sessionId="s1" sessionTitle="t" open onOpenChange={() => {}} />)
    await waitFor(() => expect(screen.getByText(/无记忆/)).toBeTruthy())
  })

  it('filters client-side case-insensitively', async () => {
    vi.mocked(api.listMemories).mockResolvedValue([mem('Uses SQLite', 0), mem('偏好中文回复', 1)])
    render(<MemoriesDialog sessionId="s1" sessionTitle="t" open onOpenChange={() => {}} />)
    await waitFor(() => expect(screen.getByText(/Uses SQLite/)).toBeTruthy())
    fireEvent.change(screen.getByPlaceholderText(/过滤/i), { target: { value: 'SQLITE' } })
    expect(screen.getByText(/Uses SQLite/)).toBeTruthy()
    expect(screen.queryByText(/偏好中文回复/)).toBeNull()
  })

  it('shows error with retry', async () => {
    vi.mocked(api.listMemories).mockRejectedValueOnce(new Error('boom'))
    render(<MemoriesDialog sessionId="s1" sessionTitle="t" open onOpenChange={() => {}} />)
    await waitFor(() => expect(screen.getByText(/boom|加载失败/)).toBeTruthy())
    vi.mocked(api.listMemories).mockResolvedValue([mem('ok', 0)])
    fireEvent.click(screen.getByRole('button', { name: /重试/i }))
    await waitFor(() => expect(screen.getByText(/ok/)).toBeTruthy())
  })
})
```

- [ ] **Step 1.2: 跑测试确认 RED**：`cd frontend && npx vitest run src/components/MemoriesDialog.test.tsx` → FAIL（模块不存在）

- [ ] **Step 1.3: types.ts 加 Memory**（PendingGoal 之前）：

```ts
export interface Memory {
  id: string
  session_id: string
  owner_id: string
  content: string
  source_run_id?: string
  created_at: number
}
```

- [ ] **Step 1.4: api.ts 加 listMemories**（listRuns 之后；types import 加 `Memory`）：

```ts
  async listMemories(sessionId: string, limit = 200): Promise<Memory[]> {
    try {
      const data = await this.request<{ memories: Memory[] }>(
        'GET',
        `/api/v1/sessions/${encodeURIComponent(sessionId)}/memories?limit=${limit}`,
      )
      return data.memories ?? []
    } catch (err) {
      if (err instanceof ApiError && err.code === 'not_found') return []
      throw err
    }
  }
```

- [ ] **Step 1.5: 新建 MemoriesDialog.tsx**

```tsx
import { useCallback, useEffect, useState } from 'react'
import { Brain, RefreshCw } from 'lucide-react'
import { api } from '@/lib/api'
import type { Memory } from '@/lib/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle,
} from '@/components/ui/dialog'

function relTime(ts: number): string {
  if (!ts) return ''
  const diff = Date.now() - ts
  const m = Math.floor(diff / 60000)
  if (m < 1) return '刚刚'
  if (m < 60) return `${m} 分钟前`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h} 小时前`
  return `${Math.floor(h / 24)} 天前`
}

export function MemoriesDialog({ sessionId, sessionTitle, open, onOpenChange }: {
  sessionId: string
  sessionTitle: string
  open: boolean
  onOpenChange: (v: boolean) => void
}) {
  const [memories, setMemories] = useState<Memory[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [filter, setFilter] = useState('')

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      setMemories(await api.listMemories(sessionId))
    } catch (e) {
      setError(e instanceof Error ? e.message : '加载失败')
    } finally {
      setLoading(false)
    }
  }, [sessionId])

  useEffect(() => {
    if (open) void load()
  }, [open, load])

  const kw = filter.trim().toLowerCase()
  const shown = kw ? memories.filter((m) => m.content.toLowerCase().includes(kw)) : memories

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[70vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-1.5">
            <Brain className="h-4 w-4" /> 会话记忆{sessionTitle ? ` · ${sessionTitle}` : ''}
          </DialogTitle>
          <DialogDescription>Agent 在本会话中沉淀的记忆（最新在前）。</DialogDescription>
        </DialogHeader>
        <div className="flex items-center gap-2">
          <Input value={filter} onChange={(e) => setFilter(e.target.value)} placeholder="过滤记忆…" className="h-8 text-xs" />
          <Button variant="ghost" size="icon" className="h-7 w-7" onClick={() => void load()} title="刷新">
            <RefreshCw className="h-3.5 w-3.5" />
          </Button>
        </div>
        {loading && <div className="text-xs text-muted-foreground">加载中…</div>}
        {error && (
          <div className="flex items-center gap-2 text-xs text-red-400">
            <span>{error}</span>
            <Button variant="ghost" size="sm" className="h-6 px-2" onClick={() => void load()}>重试</Button>
          </div>
        )}
        {!loading && !error && shown.length === 0 && (
          <div className="text-xs text-muted-foreground/70">（无记忆）</div>
        )}
        <div className="flex flex-col gap-2">
          {shown.map((m) => (
            <div key={m.id} className="rounded border border-border bg-muted/30 p-2">
              <div className="whitespace-pre-wrap text-xs text-foreground">{m.content}</div>
              <div className="mt-1 flex items-center gap-2 text-[10px] text-muted-foreground/70">
                <span>{relTime(m.created_at)}</span>
                {m.source_run_id && <span title={m.source_run_id}>run {m.source_run_id.slice(0, 8)}</span>}
              </div>
            </div>
          ))}
        </div>
      </DialogContent>
    </Dialog>
  )
}
```

- [ ] **Step 1.6: SessionList.tsx 接入**：import `Brain`（lucide）+ `MemoriesDialog`；state `const [memoriesTarget, setMemoriesTarget] = useState<{ id: string; title: string } | null>(null)`；会话行删除按钮之前加：

```tsx
                <button
                  onClick={() => setMemoriesTarget({ id: s.session_id, title: s.title })}
                  title="会话记忆"
                  className="hidden h-6 w-6 shrink-0 items-center justify-center rounded text-muted-foreground/70 hover:bg-muted hover:text-foreground group-hover:flex"
                >
                  <Brain className="h-3 w-3" />
                </button>
```

aside 内（删除 Dialog 后）渲染：

```tsx
      <MemoriesDialog
        sessionId={memoriesTarget?.id ?? ''}
        sessionTitle={memoriesTarget?.title ?? ''}
        open={memoriesTarget !== null}
        onOpenChange={(v) => !v && setMemoriesTarget(null)}
      />
```

- [ ] **Step 1.7: SessionList.test.tsx 增补**：一条用例 —— hover（`fireEvent.mouseEnter` 会话行或直接 `fireEvent.click(screen.getByTitle('会话记忆'))`）后出现「会话记忆」标题的 Dialog。
- [ ] **Step 1.8: GREEN**：`npx vitest run src/components/MemoriesDialog.test.tsx src/components/SessionList.test.tsx` → PASS
- [ ] **Step 1.9: Commit**：`git add -A frontend/src && git commit -m "feat(frontend): session memories dialog (batch A)"`

## Chunk 2: 批B 子 agent 执行树

### Task 2: derive 层 system-subagent

**Files:** Modify `frontend/src/lib/types.ts`（Run 加字段）、`frontend/src/lib/chat-messages.ts`；Test `frontend/src/lib/chat-messages.test.ts`

- [ ] **Step 2.1: 写失败测试**（chat-messages.test.ts 增补；构造 events 用 `mkEvent` 同现有 helper 风格）：

```ts
it('tool.completed task/tasks produce system-subagent message', () => {
  const run = baseRun({ id: 'r1', session_id: 's1' })
  const evs = [
    mkEvent(1, 'tool.completed', JSON.stringify({ text: 'task', tool: 'task' })),
    mkEvent(2, 'tool.completed', JSON.stringify({ text: 'file_search', tool: 'file_search' })),
    mkEvent(3, 'tool.completed', JSON.stringify({ text: 'tasks', tool: 'tasks' })),
  ]
  const msgs = deriveMessages({ runHistory: [run], activeRun: null, activeSteps: [], eventsByRun: { r1: evs }, pendingGoals: [], now: 0 })
  const sub = msgs.find((m) => m.kind === 'system-subagent')
  expect(sub).toBeTruthy()
  expect((sub as { count: number }).count).toBe(2)
})

it('plain tools produce no system-subagent', () => { /* 同上仅 file_search → find 为 undefined */ })
```

（helper 名以现有测试文件为准；无 mkEvent/baseRun 则按 ApprovalCard.test.tsx 的 `ev()` 风格内联构造 AgentEvent 与 Run。）

- [ ] **Step 2.2: RED**：`npx vitest run src/lib/chat-messages.test.ts` → FAIL
- [ ] **Step 2.3: 实现**：types.ts `Run` 在 `session_id` 后加 `parent_run_id?: string`；chat-messages.ts：

ChatMessage 联合类型加（`system-approval` 之前）：

```ts
  | {
      kind: 'system-subagent'
      id: string
      runId: string
      run: Run
      count: number
    }
```

emitRun 内（`const events = eventsByRun[run.id] ?? []` 之后、system-events push 之前）：

```ts
  let subagentCount = 0
  for (const e of events) {
    if (e.event_type !== 'tool.completed') continue
    const t = eventPayload(e).tool
    if (t === 'task' || t === 'tasks') subagentCount++
  }
  if (subagentCount > 0) {
    messages.push({ kind: 'system-subagent', id: `subagent-${run.id}`, runId: run.id, run, count: subagentCount })
  }
```

- [ ] **Step 2.4: GREEN + Commit**：`git commit -m "feat(frontend): derive system-subagent message from task/tasks tool events"`

### Task 3: SubagentCard 组件

**Files:** Create `frontend/src/components/Messages/SubagentCard.tsx`；Modify `frontend/src/components/ChatMessageList.tsx`、`frontend/src/components/Messages/index.ts`；Test `frontend/src/components/Messages/SubagentCard.test.tsx`

- [ ] **Step 3.1: 写失败测试**

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { SubagentCard } from '@/components/Messages/SubagentCard'
import { api } from '@/lib/api'
import type { Run } from '@/lib/types'

vi.mock('@/lib/api', () => ({ api: { listRuns: vi.fn() } }))

function childRun(goal: string, i: number, over: Partial<Run> = {}): Run {
  return { id: `c${i}`, session_id: 's1', owner_id: 'o1', status: 'succeeded', mode: 'react',
    goal, budget: { max_duration_seconds: 0, max_model_calls: 0, max_tool_calls: 0, max_iterations: 0, max_output_bytes: 0 },
    attempt: 1, created_at: 1000 + i, updated_at: 2000 + i, parent_run_id: 'r1', ...over }
}
const parent = { id: 'r1', session_id: 's1', status: 'succeeded' } as Run

describe('SubagentCard', () => {
  beforeEach(() => vi.mocked(api.listRuns).mockReset())

  it('collapsed shows count and expands to fetch children', async () => {
    vi.mocked(api.listRuns).mockResolvedValue([childRun('g1', 1), childRun('g2', 2)])
    render(<SubagentCard message={{ kind: 'system-subagent', id: 'x', runId: 'r1', run: parent, count: 2 }} />)
    expect(screen.getByText(/子 agent ×2/)).toBeTruthy()
    fireEvent.click(screen.getByRole('button'))
    await waitFor(() => expect(screen.getByText(/g1/)).toBeTruthy())
    expect(screen.getByText(/g2/)).toBeTruthy()
    expect(vi.mocked(api.listRuns)).toHaveBeenCalledWith('s1', 200)
  })

  it('shows failed error_code and duration', async () => {
    vi.mocked(api.listRuns).mockResolvedValue([childRun('bad', 1, { status: 'failed', error_code: 'model_failed', created_at: 1000, updated_at: 3500 })])
    render(<SubagentCard message={{ kind: 'system-subagent', id: 'x', runId: 'r1', run: parent, count: 1 }} />)
    fireEvent.click(screen.getByRole('button'))
    await waitFor(() => expect(screen.getByText(/model_failed/)).toBeTruthy())
    expect(screen.getByText(/2s|3s/)).toBeTruthy() // (3500-1000)/1000
  })

  it('empty children state', async () => {
    vi.mocked(api.listRuns).mockResolvedValue([])
    render(<SubagentCard message={{ kind: 'system-subagent', id: 'x', runId: 'r1', run: parent, count: 1 }} />)
    fireEvent.click(screen.getByRole('button'))
    await waitFor(() => expect(screen.getByText(/无子 run/)).toBeTruthy())
  })

  it('error + retry', async () => {
    vi.mocked(api.listRuns).mockRejectedValueOnce(new Error('boom'))
    render(<SubagentCard message={{ kind: 'system-subagent', id: 'x', runId: 'r1', run: parent, count: 1 }} />)
    fireEvent.click(screen.getByRole('button'))
    await waitFor(() => expect(screen.getByText(/boom|加载失败/)).toBeTruthy())
    vi.mocked(api.listRuns).mockResolvedValue([childRun('g1', 1)])
    fireEvent.click(screen.getByRole('button', { name: /重试/i }))
    await waitFor(() => expect(screen.getByText(/g1/)).toBeTruthy())
  })
})
```

- [ ] **Step 3.2: RED** → FAIL
- [ ] **Step 3.3: 实现 SubagentCard.tsx**

```tsx
import { useState } from 'react'
import { Bot, ChevronDown, ChevronRight, RefreshCw } from 'lucide-react'
import { api } from '@/lib/api'
import { runStatusClasses, truncate } from '@/lib/status'
import type { Run } from '@/lib/types'
import { cn } from '@/lib/utils'

type SubagentMessage = { kind: 'system-subagent'; id: string; runId: string; run: Run; count: number }

function durationSec(r: Run): number {
  const s = r.started_at ?? r.created_at
  const e = r.finished_at ?? r.updated_at
  if (!s || !e || e <= s) return 0
  return Math.floor((e - s) / 1000)
}

export function SubagentCard({ message }: { message: SubagentMessage }) {
  const [open, setOpen] = useState(false)
  const [children, setChildren] = useState<Run[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  async function load() {
    setLoading(true)
    setError('')
    try {
      const all = await api.listRuns(message.run.session_id, 200)
      setChildren(all.filter((r) => r.parent_run_id === message.runId).sort((a, b) => a.created_at - b.created_at))
    } catch (e) {
      setError(e instanceof Error ? e.message : '加载失败')
    } finally {
      setLoading(false)
    }
  }

  function toggle() {
    const next = !open
    setOpen(next)
    if (next && children.length === 0 && !error) void load()
  }

  return (
    <div className="rounded-lg border border-border bg-card/60 px-3 py-2">
      <div className="flex items-center gap-2">
        <button onClick={toggle} className="flex min-w-0 flex-1 items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground">
          {open ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
          <Bot className="h-3.5 w-3.5" />
          <span>子 agent ×{message.count}</span>
        </button>
        {open && (
          <button onClick={() => void load()} title="刷新" className="text-muted-foreground/70 hover:text-foreground">
            <RefreshCw className={cn('h-3 w-3', loading && 'animate-spin')} />
          </button>
        )}
      </div>
      {open && (
        <div className="mt-2 flex flex-col gap-1.5 border-t border-border pt-2">
          {loading && <div className="text-xs text-muted-foreground">加载中…</div>}
          {error && (
            <div className="flex items-center gap-2 text-xs text-red-400">
              <span>{error}</span>
              <button className="underline" onClick={() => void load()}>重试</button>
            </div>
          )}
          {!loading && !error && children.length === 0 && (
            <div className="text-xs text-muted-foreground/70">（无子 run 记录）</div>
          )}
          {children.map((r) => (
            <div key={r.id} className="flex items-center gap-2 text-xs">
              <span className={cn('inline-flex w-fit shrink-0 rounded-full border px-1.5 py-0.5 text-[10px]', runStatusClasses[r.status])}>{r.status}</span>
              <span className="min-w-0 flex-1 truncate text-foreground">{truncate(r.goal || r.id, 60)}</span>
              {r.error_code && <span className="shrink-0 text-red-400">{r.error_code}</span>}
              <span className="shrink-0 text-muted-foreground/70">{durationSec(r)}s</span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 3.4: 接线**：ChatMessageList.tsx import + `case 'system-subagent': return <SubagentCard message={message} />`（system-events case 之前）；Messages/index.ts 加 `export { SubagentCard } from './SubagentCard'`
- [ ] **Step 3.5: GREEN + Commit**：`git commit -m "feat(frontend): subagent execution tree card (batch B)"`

## Chunk 3: 批C 后端载荷增强

### Task 4: 观测 stash（TDD，纯单测）

**Files:** Modify `backend/internal/runtime/runner_eino.go`（EinoRunner 字段+方法、gatewayTool.onDone、InvokableRun 两处回调）、`backend/internal/runtime/runner_child.go`（构造点接线）；Test `backend/internal/runtime/runner_eino_test.go`（增补）

- [ ] **Step 4.1: 写失败测试**（runner_eino_test.go 末尾；该文件已 import json/testing）

```go
func TestRecordTakeToolObserved(t *testing.T) {
	r := NewEinoRunner(nil, nil, nil, nil)
	r.recordToolObserved("r1", "file_search", json.RawMessage(`{"pattern":"x"}`), "out1")
	obs, ok := r.takeToolObserved("r1", "file_search")
	if !ok || obs.args != `{"pattern":"x"}` || obs.output != "out1" {
		t.Fatalf("got ok=%v obs=%+v", ok, obs)
	}
	if _, ok := r.takeToolObserved("r1", "file_search"); ok {
		t.Fatal("consume-once violated: second take returned value")
	}
}

func TestToolObsIsolatedByRun(t *testing.T) {
	r := NewEinoRunner(nil, nil, nil, nil)
	r.recordToolObserved("rA", "file_search", json.RawMessage(`{"a":1}`), "outA")
	r.recordToolObserved("rB", "file_search", json.RawMessage(`{"b":1}`), "outB")
	obsA, ok := r.takeToolObserved("rA", "file_search")
	if !ok || obsA.output != "outA" {
		t.Fatalf("run isolation broken: %+v", obsA)
	}
	obsB, _ := r.takeToolObserved("rB", "file_search")
	if obsB.output != "outB" {
		t.Fatalf("run isolation broken: %+v", obsB)
	}
}

func TestToolObsMapBounded(t *testing.T) {
	r := NewEinoRunner(nil, nil, nil, nil)
	for i := 0; i < 300; i++ {
		r.recordToolObserved(fmt.Sprintf("r%d", i), "tool", json.RawMessage(`{}`), "o")
	}
	r.toolObsMu.Lock()
	n := len(r.toolObs)
	r.toolObsMu.Unlock()
	if n > 256 {
		t.Fatalf("map unbounded: %d", n)
	}
}
```

（fmt 未 import 则补。）

- [ ] **Step 4.2: RED**：`cd backend && go test ./internal/runtime/ -run TestToolObs -v` → 编译失败（方法未定义）
- [ ] **Step 4.3: 实现** runner_eino.go：

EinoRunner struct（`needApprovalByRun` 字段后）加：

```go
	toolObsMu sync.Mutex
	toolObs   map[string]toolObservation
```

（sync 已 import。）类型与方法（放 needApprovalHandler 之后）：

```go
// toolObservation 记录一次成功工具调用的 args/output 摘要（各截 1KB）。
type toolObservation struct {
	args   string
	output string
}

// recordToolObserved 由 gatewayTool 在 Execute 成功后调用；按 runID|toolName 隔离
// （并行子 agent 同名工具互不串扰）。map 超过 256 条整体清空，防长期运行泄漏。
func (r *EinoRunner) recordToolObserved(runID, toolName string, args json.RawMessage, output string) {
	const maxObs = 1024
	a := string(args)
	if len(a) > maxObs {
		a = a[:maxObs]
	}
	o := output
	if len(o) > maxObs {
		o = o[:maxObs]
	}
	r.toolObsMu.Lock()
	if r.toolObs == nil {
		r.toolObs = map[string]toolObservation{}
	}
	if len(r.toolObs) >= 256 {
		r.toolObs = map[string]toolObservation{}
	}
	r.toolObs[runID+"|"+toolName] = toolObservation{args: a, output: o}
	r.toolObsMu.Unlock()
}

// takeToolObserved 取出即删除（consume-once）；resume 重放工具消息时无 stash，自然降级。
func (r *EinoRunner) takeToolObserved(runID, toolName string) (toolObservation, bool) {
	r.toolObsMu.Lock()
	defer r.toolObsMu.Unlock()
	obs, ok := r.toolObs[runID+"|"+toolName]
	if ok {
		delete(r.toolObs, runID+"|"+toolName)
	}
	return obs, ok
}
```

gatewayTool struct 加字段：

```go
	// onDone 在 Execute 成功后回调（记录 args/output 摘要；失败/需审批不回调）。
	onDone func(args json.RawMessage, output string)
```

InvokableRun：approved 成功路径 `return out.Output, nil`（现 L188）改为：

```go
		if g.onDone != nil {
			g.onDone(raw, out.Output)
		}
		return out.Output, nil
```

normal 成功路径 `return out.Output, nil`（现 L200）同样加三行。

runner_child.go 构造点（L97-99）改为：

```go
		tools = append(tools, &gatewayTool{def: d, runID: runID, approved: approved,
			executor: r.executor, onNeedApproval: r.needApprovalHandler(runID),
			maxOutputBytes: budget.MaxOutputBytes,
			onDone: func(args json.RawMessage, output string) {
				r.recordToolObserved(runID, d.Name, args, output)
			}})
```

（`d := d` 循环变量已在 L96 捕获。）

- [ ] **Step 4.4: GREEN**：`go test ./internal/runtime/ -count=1` → PASS（含既有 emit 测试）
- [ ] **Step 4.5: Commit**：`git commit -m "feat(runtime): record tool args/output observations keyed by runID"`

### Task 5: mergeEventPayload + drain 接线 + 集成断言

**Files:** Modify `backend/internal/runtime/runtime.go`（helper + OnEvent）、`backend/internal/runtime/runner_eino.go`（drain L317-320）；Create `backend/internal/runtime/payload_test.go`；Modify `backend/tests/integration/builtin_tools_test.go`

- [ ] **Step 5.1: 写失败测试** `backend/internal/runtime/payload_test.go`

```go
package runtime

import "testing"

func TestMergeEventPayload(t *testing.T) {
	// 无 extra：与现状字节一致（map 序列化键有序 text,tool）
	if got := mergeEventPayload("hello", "", ""); got != `{"text":"hello","tool":""}` {
		t.Fatalf("baseline changed: %s", got)
	}
	// extra 合并，extra 键胜出
	got := mergeEventPayload("task", "task", `{"args":"{}","output":"ok"}`)
	want := `{"args":"{}","output":"ok","text":"task","tool":"task"}`
	if got != want {
		t.Fatalf("merge wrong:\n got %s\nwant %s", got, want)
	}
	// 非法 extra 忽略
	if got := mergeEventPayload("a", "b", "{not json"); got != `{"text":"a","tool":"b"}` {
		t.Fatalf("invalid extra must be ignored: %s", got)
	}
}
```

- [ ] **Step 5.2: RED**：`go test ./internal/runtime/ -run TestMergeEventPayload` → 编译失败
- [ ] **Step 5.3: 实现 runtime.go**：OnEvent（L42-48）改为：

```go
	deps.Runner.OnEvent(func(ev RuntimeEvent) {
		payload := mergeEventPayload(ev.Text, ev.ToolName, ev.Payload)
		if _, err := deps.Events.Append(context.Background(), ev.RunID, ev.Type, payload, "normal"); err != nil {
			deps.Log.Warn("event append failed", "run_id", ev.RunID, "type", ev.Type, "err", err)
			observability.IncrEventAppendFailed()
		}
	})
```

New() 之后加 helper（json 已 import）：

```go
// mergeEventPayload 合并基础 {"text","tool"} 与 extra JSON（extra 键胜出；非法 extra 忽略）。
// 现有全部 emit 调用 extra 均为空 → 输出与历史字节一致。
func mergeEventPayload(text, tool, extra string) string {
	m := map[string]string{"text": text, "tool": tool}
	if extra != "" {
		var e map[string]string
		if err := json.Unmarshal([]byte(extra), &e); err == nil {
			for k, v := range e {
				m[k] = v
			}
		}
	}
	b, _ := json.Marshal(m)
	return string(b)
}
```

runner_eino.go drain L317-320 改为：

```go
			if mo.ToolName != "" {
				lastTool = mo.ToolName
				extra := ""
				if obs, ok := r.takeToolObserved(runID, mo.ToolName); ok {
					if b, err := json.Marshal(map[string]string{"args": obs.args, "output": obs.output}); err == nil {
						extra = string(b)
					}
				}
				r.emit(ctx, out, runID, domain.EvtToolCompleted, mo.ToolName, mo.ToolName, extra)
			}
```

- [ ] **Step 5.4: GREEN + 集成断言**：`go test ./internal/runtime/ -count=1` PASS 后，builtin_tools_test.go 增：

```go
func TestToolCompletedPayloadHasArgsOutput(t *testing.T) {
	h := testutil.New(t)
	ses := h.CreateSession("s")
	h.Model.EnqueueToolCall("tc1", "file_search", `{"query":"x"}`)
	h.Model.EnqueueText("done")
	id, code, _ := h.CreateRun(ses, fixturePayload(t, ses), "tcp-1")
	if code != 201 {
		t.Fatalf("create run: %d", code)
	}
	run := h.WaitTerminal(id, 30*time.Second)
	if run.Status != domain.RunSucceeded {
		t.Fatalf("status=%s", run.Status)
	}
	evs, err := h.Events.ListAfter(context.Background(), id, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Type != domain.EvtToolCompleted {
			continue
		}
		var p map[string]any
		if json.Unmarshal([]byte(e.PayloadJSON), &p) != nil {
			continue
		}
		if p["tool"] == "file_search" {
			if _, ok := p["args"]; !ok {
				t.Fatalf("tool.completed missing args: %s", e.PayloadJSON)
			}
			if _, ok := p["output"]; !ok {
				t.Fatalf("tool.completed missing output: %s", e.PayloadJSON)
			}
			return
		}
	}
	t.Fatal("no tool.completed for file_search")
}
```

（imports 以文件现状为准：context/encoding/json/time/domain/testutil 大多已在。）运行 `go test ./tests/integration/ -run TestToolCompletedPayloadHasArgsOutput -count=1` → PASS。

- [ ] **Step 5.5: 回归**：`go test ./... -count=1` 全绿（evals 15 场景计数不变）
- [ ] **Step 5.6: Commit**：`git commit -m "feat(runtime): tool.completed events carry args/output summary (merge via ev.Payload)"`

## Chunk 4: 批C 前端富渲染 + 收尾

### Task 6: system-tools derive + ToolCallsCard

**Files:** Modify `frontend/src/lib/chat-messages.ts`、`frontend/src/components/ChatMessageList.tsx`、`frontend/src/components/Messages/index.ts`；Create `frontend/src/components/Messages/ToolCallsCard.tsx`；Test `frontend/src/components/Messages/ToolCallsCard.test.tsx`、`frontend/src/lib/chat-messages.test.ts`

- [ ] **Step 6.1: 写失败测试**

chat-messages.test.ts 增补：

```ts
it('tool.completed with args/output produces system-tools message', () => {
  const run = baseRun({ id: 'r1', session_id: 's1' })
  const evs = [
    mkEvent(1, 'tool.completed', JSON.stringify({ text: 'file_search', tool: 'file_search', args: '{"query":"x"}', output: 'hit' })),
    mkEvent(2, 'tool.completed', JSON.stringify({ text: 'legacy', tool: 'legacy' })),
  ]
  const msgs = deriveMessages({ runHistory: [run], activeRun: null, activeSteps: [], eventsByRun: { r1: evs }, pendingGoals: [], now: 0 })
  const t = msgs.find((m) => m.kind === 'system-tools')
  expect(t).toBeTruthy()
  expect((t as { calls: unknown[] }).calls).toHaveLength(1)
  expect((t as { calls: { tool: string }[] }).calls[0].tool).toBe('file_search')
})
```

ToolCallsCard.test.tsx（新建）：

```tsx
import { describe, it, expect } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { ToolCallsCard } from '@/components/Messages/ToolCallsCard'

const calls = [
  { tool: 'file_search', args: '{"query":"x"}', output: 'line1\nline2', seq: 1 },
  { tool: 'http_fetch', args: '{"url":"http://a"}', output: '{"status":200}', seq: 2 },
]

describe('ToolCallsCard', () => {
  it('renders collapsed summary and expands entries', () => {
    render(<ToolCallsCard message={{ kind: 'system-tools', id: 'x', runId: 'r1', calls }} />)
    expect(screen.getByText(/工具调用 ×2/)).toBeTruthy()
    fireEvent.click(screen.getByRole('button'))
    expect(screen.getByText(/file_search/)).toBeTruthy()
    expect(screen.getByText(/http_fetch/)).toBeTruthy()
  })

  it('returns null for empty calls', () => {
    const { container } = render(<ToolCallsCard message={{ kind: 'system-tools', id: 'x', runId: 'r1', calls: [] }} />)
    expect(container.firstChild).toBeNull()
  })
})
```

- [ ] **Step 6.2: RED**：`npx vitest run src/lib/chat-messages.test.ts src/components/Messages/ToolCallsCard.test.tsx` → FAIL
- [ ] **Step 6.3: 实现**：chat-messages.ts 联合类型加（system-subagent 之后）：

```ts
  | {
      kind: 'system-tools'
      id: string
      runId: string
      calls: { tool: string; args?: string; output?: string; seq: number }[]
    }
```

emitRun（subagent 块之后、system-events 之前）：

```ts
  const toolCalls: { tool: string; args?: string; output?: string; seq: number }[] = []
  for (const e of events) {
    if (e.event_type !== 'tool.completed') continue
    const p = eventPayload(e)
    const args = typeof p.args === 'string' ? p.args : undefined
    const output = typeof p.output === 'string' ? p.output : undefined
    if (args === undefined && output === undefined) continue
    toolCalls.push({ tool: typeof p.tool === 'string' ? p.tool : '', args, output, seq: e.seq })
  }
  if (toolCalls.length > 0) {
    messages.push({ kind: 'system-tools', id: `tools-${run.id}`, runId: run.id, calls: toolCalls })
  }
```

ToolCallsCard.tsx：

```tsx
import { useState } from 'react'
import { ChevronDown, ChevronRight, Wrench } from 'lucide-react'
import type { Run } from '@/lib/types'

type ToolsMessage = {
  kind: 'system-tools'
  id: string
  runId: string
  calls: { tool: string; args?: string; output?: string; seq: number }[]
}
type RunRef = Run // 仅占位保持类型导入一致（如 lint 报未用可移除）

export function ToolCallsCard({ message }: { message: ToolsMessage }) {
  const [open, setOpen] = useState(false)
  if (message.calls.length === 0) return null
  return (
    <div className="rounded-lg border border-border bg-card/60 px-3 py-2">
      <button onClick={() => setOpen(!open)} className="flex w-full items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground">
        {open ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
        <Wrench className="h-3.5 w-3.5" />
        <span>工具调用 ×{message.calls.length}</span>
      </button>
      {open && (
        <div className="mt-2 flex flex-col gap-2 border-t border-border pt-2">
          {message.calls.map((c) => (
            <div key={c.seq} className="rounded border border-border bg-muted/30 p-2">
              <div className="text-[11px] font-medium text-teal-400">{c.tool}</div>
              {c.args && <pre className="mt-1 max-h-24 overflow-y-auto whitespace-pre-wrap break-all text-[10px] text-muted-foreground">{c.args}</pre>}
              {c.output && <pre className="mt-1 max-h-32 overflow-y-auto whitespace-pre-wrap break-all text-[10px] text-foreground/80">{c.output}</pre>}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
```

（若 `RunRef` 触发 unused 告警则直接删除该行与 Run 导入。）

ChatMessageList.tsx：import ToolCallsCard + `case 'system-tools': return <ToolCallsCard message={message} />`；Messages/index.ts 导出。

- [ ] **Step 6.4: GREEN**：`npx vitest run` 全量 → PASS
- [ ] **Step 6.5: Commit**：`git commit -m "feat(frontend): tool calls detail card from enriched events (batch C)"`

### Task 7: 文档同步 + 全量验收

**Files:** Modify `frontend/README.md`、`frontend/README.en.md`（组件清单/功能说明各补 2-3 行）、`backend/README.md`、`backend/README.en.md`（事件说明补一句：tool.completed payload 含 args/output 摘要，各 1KB 截断）

- [ ] **Step 7.1: 文档编辑**（简短，不展开小节）
- [ ] **Step 7.2: 前端验收**：`cd frontend && npx tsc --noEmit`（0 错误）+ `npx vitest run`（全绿，用例 >127）
- [ ] **Step 7.3: 后端验收**：`cd backend && go build ./... && go vet ./... && go test ./... -count=1 -timeout 600s`（全绿）+ `make test-eval`（15 场景 pass_rate 1）
- [ ] **Step 7.4: 后端零漂移检查**：`git diff --stat d34db6c..HEAD -- backend/internal/api backend/internal/store backend/internal/domain` → 应为空（批C 只动 runtime 包）
- [ ] **Step 7.5: Commit**：`git add -A && git commit -m "docs: frontend completion + tool payload enrichment notes (zh/en)"`

---

## Self-Review 记录（写计划时已核）

- ChatMessage 联合类型两处扩展（system-subagent/system-tools）已入 Task 2/6 ✓
- InvokableRun 两条成功路径都要回调 onDone（Task 4 显式列出）✓
- `d := d` 捕获已存在于 runner_child.go L96，onDone 闭包安全 ✓
- mergeEventPayload 基线断言 `{"text":"hello","tool":""}` 与现状字节一致（map 键序 text<tool 字母序）✓
- consume-once + resume 重放降级、map 256 上限、runID 隔离均有测试 ✓
- 集成断言用 file_search（read/never 工具，无需审批，harness 默认 New 即可）✓
- 前端 helper 名（mkEvent/baseRun）以 chat-messages.test.ts 现状为准，无则内联构造 ✓
