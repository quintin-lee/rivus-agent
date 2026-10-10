import { render, screen, fireEvent } from '@testing-library/react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { EventStream } from './EventStream'
import { useRunStore } from '@/store/run-store'
import { useUiStore } from '@/store/ui-store'
import type { AgentEvent } from '@/lib/types'

vi.mock('@/lib/api', () => ({
  api: {
    decideApproval: vi.fn().mockResolvedValue(undefined),
    getRun: vi.fn().mockResolvedValue({ run: {}, steps: [] }),
    fetchRunAndSteps: vi.fn().mockResolvedValue(undefined),
  },
}))
vi.mock('@/lib/status', () => ({
  eventCategory: (t: string) => t.split('.')[0],
  eventCategoryClasses: {
    run: 'c-run',
    model: 'c-model',
    tool: 'c-tool',
    step: 'c-step',
    approval: 'c-approval',
    other: 'c-other',
  },
  formatTimestamp: () => '12:00:00',
}))

function ev(seq: number, type: AgentEvent['event_type'], payload = '{}'): AgentEvent {
  return {
    seq,
    run_id: 'r1',
    event_type: type,
    payload_json: payload,
    sensitivity: 'normal',
    created_at: 0,
  }
}

describe('EventStream', () => {
  beforeEach(() => {
    useUiStore.setState({ eventStreamOpen: true })
    useRunStore.setState({ events: [], eventFilter: 'all' })
  })

  it('renders each event with its type badge and summary', () => {
    useRunStore.setState({
      events: [ev(1, 'run.started'), ev(2, 'model.requested', '{"call_count":3}')],
      eventFilter: 'all',
    })
    render(<EventStream />)
    expect(screen.getByText('run.started')).toBeInTheDocument()
    expect(screen.getByText(/model call 3/i)).toBeInTheDocument()
  })

  it('filters events by category chip', () => {
    useRunStore.setState({
      events: [ev(1, 'run.started'), ev(2, 'tool.completed', '{"tool_name":"search"}')],
      eventFilter: 'all',
    })
    render(<EventStream />)
    fireEvent.click(screen.getByText('tool'))
    expect(screen.getByText(/tool: search/i)).toBeInTheDocument()
    expect(screen.queryByText('run.started')).toBeNull()
  })

  it('filters by search query over payload_json', () => {
    useRunStore.setState({
      events: [
        ev(1, 'tool.completed', '{"tool_name":"alpha"}'),
        ev(2, 'tool.completed', '{"tool_name":"beta"}'),
      ],
    })
    render(<EventStream />)
    fireEvent.change(screen.getByPlaceholderText(/Search/i), {
      target: { value: 'alpha' },
    })
    expect(screen.getByText(/tool: alpha/i)).toBeInTheDocument()
    expect(screen.queryByText(/tool: beta/i)).toBeNull()
  })

  it('renders ApprovalCard under approval.requested event', () => {
    useRunStore.setState({
      events: [ev(1, 'approval.requested', '{"approval_id":"a1","tool_name":"publish"}')],
    })
    render(<EventStream />)
    expect(screen.getByText(/Approval required/i)).toBeInTheDocument()
    expect(screen.getAllByText(/publish/i).length).toBeGreaterThan(0)
    expect(screen.getByRole('button', { name: /Approve/i })).toBeInTheDocument()
  })

  it('shows empty state when no events', () => {
    render(<EventStream />)
    expect(screen.getByText('No events')).toBeInTheDocument()
  })
})
