import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { RunDetailDialog } from '@/components/RunDetailDialog'
import { api } from '@/lib/api'
import type { Run, Step, AgentEvent, Approval } from '@/lib/types'

vi.mock('@/lib/api', () => ({
  api: { getRun: vi.fn(), getEvents: vi.fn(), listApprovals: vi.fn() },
}))

function run(over: Partial<Run> = {}): Run {
  return {
    id: 'r1abcdef',
    session_id: 's1abcdef',
    owner_id: 'o1',
    status: 'succeeded',
    mode: 'react',
    goal: '整理周报',
    budget: { max_tool_calls: 10, max_model_calls: 20 } as Run['budget'],
    attempt: 1,
    created_at: 1700000000000,
    updated_at: 1700000000000,
    ...over,
  } as Run
}

const step: Step = {
  id: 'st1',
  run_id: 'r1abcdef',
  step_index: 1,
  description: '搜索资料',
  status: 'succeeded',
}

const toolEvent: AgentEvent = {
  seq: 3,
  run_id: 'r1abcdef',
  event_type: 'tool.completed',
  payload_json: JSON.stringify({ args: '{"query":"q"}', output: 'result-body' }),
  sensitivity: 'normal',
  created_at: 1700000001000,
}

const approval: Approval = {
  id: 'a1',
  run_id: 'r1abcdef',
  tool_call_id: 'tc1',
  tool_name: 'file_edit',
  args_hash: 'h',
  requested_by: 'agent',
  status: 'approved',
  expires_at: 0,
  created_at: 1,
}

describe('RunDetailDialog', () => {
  it('copies the run id from the header', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.assign(navigator, { clipboard: { writeText } })
    vi.mocked(api.getRun).mockResolvedValue({ run: run(), steps: [] })
    vi.mocked(api.getEvents).mockResolvedValue([])
    vi.mocked(api.listApprovals).mockResolvedValue([])
    render(<RunDetailDialog runId="r1abcdef" onOpenChange={() => {}} />)
    await waitFor(() => expect(api.getRun).toHaveBeenCalled())
    fireEvent.click(screen.getByLabelText('复制'))
    await vi.waitFor(() => expect(writeText).toHaveBeenCalledWith('r1abcdef'))
  })

  beforeEach(() => {
    vi.mocked(api.getRun).mockReset()
    vi.mocked(api.getEvents).mockReset()
    vi.mocked(api.listApprovals).mockReset()
  })

  it('fetches run, events and approvals on open and renders all sections', async () => {
    vi.mocked(api.getRun).mockResolvedValue({ run: run(), steps: [step] })
    vi.mocked(api.getEvents).mockResolvedValue([toolEvent])
    vi.mocked(api.listApprovals).mockResolvedValue([approval])
    render(<RunDetailDialog runId="r1abcdef" onOpenChange={() => {}} />)

    await waitFor(() => {
      expect(api.getRun).toHaveBeenCalledWith('r1abcdef')
      expect(api.getEvents).toHaveBeenCalledWith('r1abcdef', 0)
      expect(api.listApprovals).toHaveBeenCalledWith('r1abcdef')
    })
    expect(await screen.findByText('搜索资料')).toBeTruthy()
    expect(screen.getByText('result-body')).toBeTruthy()
    expect(screen.getByText('file_edit')).toBeTruthy()
  })

  it('renders error with retry when getRun fails', async () => {
    vi.mocked(api.getRun).mockRejectedValue(new Error('boom'))
    vi.mocked(api.getEvents).mockRejectedValue(new Error('boom'))
    vi.mocked(api.listApprovals).mockRejectedValue(new Error('boom'))
    render(<RunDetailDialog runId="r1abcdef" onOpenChange={() => {}} />)
    expect(await screen.findByText('加载失败')).toBeTruthy()
    expect(screen.getByRole('button', { name: '重试' })).toBeTruthy()
  })

  it('does not fetch when runId is null', () => {
    render(<RunDetailDialog runId={null} onOpenChange={() => {}} />)
    expect(api.getRun).not.toHaveBeenCalled()
    expect(api.getEvents).not.toHaveBeenCalled()
    expect(api.listApprovals).not.toHaveBeenCalled()
  })

  it('renders event timeline with labels and count', async () => {
    vi.mocked(api.getRun).mockResolvedValue({ run: run(), steps: [] })
    vi.mocked(api.listApprovals).mockResolvedValue([])
    const ev = (seq: number, type: AgentEvent['event_type']): AgentEvent => ({
      seq,
      run_id: 'r1abcdef',
      event_type: type,
      payload_json: '{}',
      sensitivity: 'normal',
      created_at: 1700000001000 + seq,
    })
    vi.mocked(api.getEvents).mockResolvedValue([
      ev(1, 'run.started'),
      ev(2, 'tool.completed'),
      ev(3, 'budget.consumed'),
    ])
    render(<RunDetailDialog runId="r1abcdef" onOpenChange={() => {}} />)
    await waitFor(() => expect(screen.getByText('运行开始')).toBeTruthy())
    expect(screen.getByText('工具完成')).toBeTruthy()
    expect(screen.getByText('预算消耗')).toBeTruthy()
    expect(screen.getByText('共 3 条事件')).toBeTruthy()
  })

  it('renders empty timeline without count', async () => {
    vi.mocked(api.getRun).mockResolvedValue({ run: run(), steps: [] })
    vi.mocked(api.listApprovals).mockResolvedValue([])
    vi.mocked(api.getEvents).mockResolvedValue([])
    render(<RunDetailDialog runId="r1abcdef" onOpenChange={() => {}} />)
    await waitFor(() => expect(screen.getByText('（无事件）')).toBeTruthy())
    expect(screen.queryByText(/共 \d+ 条事件/)).toBeNull()
  })
})

describe('RunDetailDialog timeline deepening', () => {
  function ev(seq: number, type: AgentEvent["event_type"], payload: Record<string, unknown> = {}): AgentEvent {
    return {
      seq,
      run_id: 'r1abcdef',
      event_type: type,
      payload_json: JSON.stringify(payload),
      sensitivity: 'normal',
      created_at: 1700000000000 + seq * 1000,
    }
  }

  beforeEach(() => {
    vi.mocked(api.getRun).mockReset()
    vi.mocked(api.getEvents).mockReset()
    vi.mocked(api.listApprovals).mockReset()
    vi.mocked(api.getRun).mockResolvedValue({ run: run(), steps: [] })
    vi.mocked(api.listApprovals).mockResolvedValue([])
  })

  it('labels all 28 event types (no raw type leak for known types)', async () => {
    const types: AgentEvent['event_type'][] = [
      'run.created','run.started','run.finished','run.failed','run.cancelled','run.timed_out',
      'plan.created','plan.updated','step.started','step.finished','model.requested',
      'model.completed','model.token','budget.consumed','budget.exceeded','model.failed',
      'tool.requested','tool.authorized','tool.denied','tool.started','tool.completed','tool.failed',
      'approval.requested','approval.decided','checkpoint.saved','run.resumed',
      'verification.passed','verification.failed',
    ]
    vi.mocked(api.getEvents).mockResolvedValue(types.map((t2, i) => ev(i + 1, t2)))
    render(<RunDetailDialog runId="r1abcdef" onOpenChange={() => {}} />)
    await waitFor(() => expect(screen.getByText('时间线')).toBeTruthy())
    // 每个事件类型都应有非原始 label（timeline.* 缺失时 t() 回退为 key 本身）
    for (const ty of types) {
      const label = screen.queryByText(ty)
      expect(label, `raw type ${ty} leaked without i18n label`).toBeNull()
    }
    expect(screen.getByText('运行创建')).toBeTruthy()
  })

  it('shows tool name on tool.* timeline rows', async () => {
    vi.mocked(api.getEvents).mockResolvedValue([ev(1, 'tool.completed', { tool: 'knowledge_search', text: 'knowledge_search' })])
    render(<RunDetailDialog runId="r1abcdef" onOpenChange={() => {}} />)
    const row = await waitFor(() => screen.getByText('工具完成')).then((el) => el.closest('div.relative') as HTMLElement)
    expect(row.textContent).toContain('knowledge_search')
  })

  it('展开全部 shows all events when more than 50', async () => {
    vi.mocked(api.getEvents).mockResolvedValue(
      Array.from({ length: 60 }, (_, i) => ev(i + 1, 'run.started'))
    )
    render(<RunDetailDialog runId="r1abcdef" onOpenChange={() => {}} />)
    // 默认折叠到最近 50 条
    await waitFor(() => expect(screen.getAllByText('运行开始').length).toBe(50))
    fireEvent.click(screen.getByRole('button', { name: /展开全部|expand/i }))
    await waitFor(() => expect(screen.getAllByText('运行开始').length).toBe(60))
  })

  it('shows step description on step.started/finished timeline rows (matched by step_index)', async () => {
    const desc = step.description // '搜索相关资料'
    vi.mocked(api.getRun).mockResolvedValue({ run: run(), steps: [step] })
    vi.mocked(api.getEvents).mockResolvedValue([
      ev(1, 'step.started', { step_index: 1 }),
      ev(2, 'step.finished', { step_index: 1 }),
      ev(3, 'step.started', { step_index: 99 }), // 匹配不到 → 省略描述
    ])
    render(<RunDetailDialog runId="r1abcdef" onOpenChange={() => {}} />)
    // 描述出现位置：时间线里 step_index=1 的 2 条匹配行（started+finished）+ 步骤列表 1 处 = 共 3 处；
    // step_index=99 匹配不到 → 该行省略描述。
    await waitFor(() => {
      expect(screen.getAllByText(desc).length).toBe(3)
    })
    const timelineHits = screen.getAllByText(desc).filter((n) => {
      const row = n.parentElement // 时间线行 div（relative flex items-center）
      if (!row) return false
      const tr = row.textContent ?? ''
      return tr.includes('步骤开始') || tr.includes('步骤完成')
    })
    expect(timelineHits.length).toBe(2)
    // step_index 99 那行：时间线里存在但没有描述
    const startedRows = Array.from(
      screen.queryAllByText('步骤开始')?.map((n) => n.parentElement) ?? [],
    ).filter((r): r is HTMLElement => !!r)
    const unknownRow = startedRows.find((r) => !(r?.textContent ?? '').includes(desc))
    expect(unknownRow).toBeDefined()
  })
})
