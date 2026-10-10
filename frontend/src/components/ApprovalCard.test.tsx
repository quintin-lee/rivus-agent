import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ApprovalCard } from './ApprovalCard'
import { useRunStore } from '@/store/run-store'
import type { AgentEvent } from '@/lib/types'

const { decideApproval } = vi.hoisted(() => ({
  decideApproval: vi.fn().mockResolvedValue(undefined),
}))

vi.mock('@/lib/api', () => ({ api: { decideApproval } }))
vi.mock('@/lib/status', () => ({ formatTimestamp: () => '12:00:00' }))

function approvalEvent(): AgentEvent {
  return {
    seq: 1,
    run_id: 'r1',
    event_type: 'approval.requested',
    payload_json: JSON.stringify({
      approval_id: 'a1',
      tool_name: 'publish',
      expires_at: Date.now() + 60000,
    }),
    sensitivity: 'normal',
    created_at: 0,
  }
}

describe('ApprovalCard', () => {
  beforeEach(() => {
    decideApproval.mockClear()
    useRunStore.setState({
      activeRun: null,
      fetchRunAndSteps: vi.fn().mockResolvedValue(undefined),
    })
  })

  it('shows tool name and approve/reject buttons', () => {
    render(<ApprovalCard event={approvalEvent()} />)
    expect(screen.getAllByText(/publish/i).length).toBeGreaterThan(0)
    expect(screen.getByRole('button', { name: /Approve/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Reject/i })).toBeInTheDocument()
  })

  it('approving calls decideApproval(true) and marks decided', async () => {
    render(<ApprovalCard event={approvalEvent()} />)
    fireEvent.click(screen.getByRole('button', { name: /Approve/i }))
    await waitFor(() =>
      expect(decideApproval).toHaveBeenCalledWith('r1', 'a1', true),
    )
    expect(await screen.findByText('approved')).toBeInTheDocument()
  })

  it('rejecting with reason calls decideApproval(false, reason)', async () => {
    render(<ApprovalCard event={approvalEvent()} />)
    fireEvent.click(screen.getByRole('button', { name: /Reject/i }))
    fireEvent.change(screen.getByPlaceholderText(/Why should/i), {
      target: { value: 'too risky' },
    })
    fireEvent.click(screen.getByRole('button', { name: /^Reject$/i }))
    await waitFor(() =>
      expect(decideApproval).toHaveBeenCalledWith('r1', 'a1', false, 'too risky'),
    )
  })

  it('hides cards whose payload has no approval_id', () => {
    render(<ApprovalCard event={{ ...approvalEvent(), payload_json: '{}' }} />)
    expect(screen.queryByText('publish')).toBeNull()
  })
})
