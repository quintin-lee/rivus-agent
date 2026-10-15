import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { ChatMessageList } from '@/components/ChatMessageList'
import { useRunStore } from '@/store/run-store'
import { api } from '@/lib/api'
import type { Run, AgentEvent, Step, PendingGoal } from '@/lib/types'

vi.mock('@/lib/api', () => ({
  api: {
    createRun: vi.fn().mockResolvedValue({ run_id: 'r1', status: 'queued', duplicated: false }),
    decideApproval: vi.fn().mockResolvedValue({ status: 'decided' }),
    cancelRun: vi.fn().mockResolvedValue({}),
    resumeRun: vi.fn().mockResolvedValue({}),
    getRun: vi.fn().mockResolvedValue({ run: null, steps: [] }),
    listRuns: vi.fn().mockResolvedValue([]),
  },
}))

function mkRun(status: Run['status'], extra: Partial<Run> = {}): Run {
  return {
    id: 'r1',
    session_id: 's1',
    owner_id: 'o1',
    status,
    mode: 'react',
    goal: 'do thing',
    budget: {
      max_duration_seconds: 60,
      max_model_calls: 10,
      max_tool_calls: 10,
      max_iterations: 5,
      max_output_bytes: 1024,
    },
    checkpoint_id: '',
    attempt: 1,
    created_at: 1,
    updated_at: 1,
    ...extra,
  }
}

function seedStores(
  activeRun: Run | null,
  activeSteps: Step[],
  events: AgentEvent[],
  pendingGoals: PendingGoal[],
) {
  useRunStore.setState({
    activeRun,
    activeSteps,
    events,
    pendingGoals,
    runHistory: activeRun ? [activeRun] : [],
  })
}

describe('ChatMessageList', () => {
  beforeEach(() => {
    useRunStore.setState({
      activeRun: null,
      activeSteps: [],
      events: [],
      pendingGoals: [],
      runHistory: [],
    })
  })

  it('renders user, system-status, assistant bubbles in order for a succeeded run', () => {
    const run = mkRun('succeeded', { result_json: '{"ok":true}' })
    seedStores(run, [], [], [])
    render(<ChatMessageList />)
    expect(screen.getAllByText('do thing').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText(/Result/i).length).toBeGreaterThanOrEqual(1)
  })

  it('renders a pending user bubble for pendingGoals and no run', () => {
    seedStores(null, [], [], [{ id: 'p1', goal: 'waiting', status: 'pending' }])
    render(<ChatMessageList />)
    expect(screen.getByText('waiting')).toBeTruthy()
    expect(screen.getByText(/Sending/i)).toBeTruthy()
  })

  it('renders approval bubble wired to api.decideApproval', async () => {
    const run = mkRun('waiting_approval')
    const approvalEvent: AgentEvent = {
      seq: 1,
      run_id: 'r1',
      event_type: 'approval.requested',
      payload_json: JSON.stringify({ approval_id: 'a1', tool_name: 'shell' }),
      sensitivity: 'normal',
      created_at: 0,
    }
    seedStores(run, [], [approvalEvent], [])
    render(<ChatMessageList />)
    fireEvent.click(screen.getByRole('button', { name: /approve/i }))
    await vi.waitFor(() =>
      expect(api.decideApproval).toHaveBeenCalledWith('r1', 'a1', true),
    )
  })
  it('renders a placeholder when there are no messages', () => {
    seedStores(null, [], [], [])
    render(<ChatMessageList />)
    expect(screen.getByText(/no tasks yet/i)).toBeTruthy()
  })

  it('does not render the placeholder when messages exist', () => {
    seedStores(null, [], [], [])
    useRunStore.setState({
      runHistory: [mkRun('succeeded', { result_json: '{"ok":true}' })],
      activeRun: null,
    })
    render(<ChatMessageList />)
    expect(screen.queryByText(/no tasks yet/i)).toBeNull()
  })
})
