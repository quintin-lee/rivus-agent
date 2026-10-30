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
