import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { ApprovalCard } from '@/components/ApprovalCard'
import type { AgentEvent } from '@/lib/types'

function ev(approvalId: string, toolName = 'shell'): AgentEvent {
  return {
    seq: 1,
    run_id: 'r1',
    event_type: 'approval.requested',
    payload_json: JSON.stringify({ approval_id: approvalId, tool_name: toolName }),
    sensitivity: 'normal',
    created_at: 0,
  }
}

describe('ApprovalCard (controlled)', () => {
  it('renders tool name and approve/reject buttons when pending', () => {
    const onApprove = vi.fn()
    const onReject = vi.fn()
    render(
      <ApprovalCard event={ev('a1', 'shell')} state="pending" onApprove={onApprove} onReject={onReject} />,
    )
    expect(screen.getByText(/shell/i)).toBeTruthy()
    expect(screen.getByRole('button', { name: /approve/i })).toBeTruthy()
    expect(screen.getByRole('button', { name: /reject/i })).toBeTruthy()
  })

  it('fires onApprove with no argument', () => {
    const onApprove = vi.fn()
    render(
      <ApprovalCard event={ev('a1')} state="pending" onApprove={onApprove} onReject={vi.fn()} />,
    )
    fireEvent.click(screen.getByRole('button', { name: /approve/i }))
    expect(onApprove).toHaveBeenCalledTimes(1)
    expect(onApprove).toHaveBeenCalledWith()
  })

  it('opens reject dialog and fires onReject with reason', async () => {
    const onReject = vi.fn()
    render(
      <ApprovalCard event={ev('a1')} state="pending" onApprove={vi.fn()} onReject={onReject} />,
    )
    fireEvent.click(screen.getByRole('button', { name: /reject/i }))
    const textarea = await screen.findByPlaceholderText(/reason/i)
    fireEvent.change(textarea, { target: { value: 'too risky' } })
    fireEvent.click(screen.getByRole('button', { name: /confirm|submit/i }))
    expect(onReject).toHaveBeenCalledWith('too risky')
  })

  it('shows a resolved badge and no action buttons when state is approved', () => {
    render(
      <ApprovalCard event={ev('a1')} state="approved" onApprove={vi.fn()} onReject={vi.fn()} />,
    )
    expect(screen.queryByRole('button', { name: /approve/i })).toBeNull()
    expect(screen.getByText(/approved/i)).toBeTruthy()
  })

  it('shows a resolved badge and no action buttons when state is rejected', () => {
    render(
      <ApprovalCard event={ev('a1')} state="rejected" onApprove={vi.fn()} onReject={vi.fn()} />,
    )
    expect(screen.queryByRole('button', { name: /reject/i })).toBeNull()
    expect(screen.getByText(/rejected/i)).toBeTruthy()
  })

  it('renders a compact single line with no buttons when expired', () => {
    render(
      <ApprovalCard event={ev('a1')} state="expired" onApprove={vi.fn()} onReject={vi.fn()} />,
    )
    expect(screen.getByText(/approval expired/i)).toBeTruthy()
    expect(screen.queryByRole('button', { name: /approve/i })).toBeNull()
  })

  it('renders a compact line with the tool name when approved', () => {
    render(
      <ApprovalCard event={ev('a1', 'shell')} state="approved" onApprove={vi.fn()} onReject={vi.fn()} />,
    )
    expect(screen.getByText(/✓ approved shell/i)).toBeTruthy()
  })

  it('renders nothing when approval_id is missing from payload', () => {
    const onApprove = vi.fn()
    const { container } = render(
      <ApprovalCard event={ev('', 'shell')} state="pending" onApprove={onApprove} onReject={vi.fn()} />,
    )
    expect(container.firstChild).toBeNull()
  })
})
