import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ActionBar } from './ActionBar'
import { useRunStore } from '@/store/run-store'
import type { Run, RunStatus } from '@/lib/types'

const { cancelRun, resumeRun } = vi.hoisted(() => ({
  cancelRun: vi.fn().mockResolvedValue(undefined),
  resumeRun: vi.fn().mockResolvedValue(undefined),
}))

vi.mock('@/lib/api', () => ({ api: { cancelRun, resumeRun } }))

function makeRun(status: RunStatus): Run {
  return {
    id: 'r1',
    session_id: 's',
    owner_id: 'default',
    status,
    mode: 'react',
    goal: 'g',
    budget: {
      max_duration_seconds: 1,
      max_model_calls: 1,
      max_tool_calls: 1,
      max_iterations: 1,
      max_output_bytes: 1,
    },
    attempt: 1,
    created_at: 0,
    updated_at: 0,
  }
}

describe('ActionBar', () => {
  beforeEach(() => {
    useRunStore.setState({ activeRun: null, fetchRunAndSteps: vi.fn().mockResolvedValue(undefined) })
  })

  it('shows Cancel for an active running run', () => {
    useRunStore.setState({ activeRun: makeRun('running') })
    render(<ActionBar />)
    expect(screen.getByRole('button', { name: /Cancel/i })).toBeInTheDocument()
  })

  it('shows Review Approval for waiting_approval', () => {
    useRunStore.setState({ activeRun: makeRun('waiting_approval') })
    render(<ActionBar />)
    expect(screen.getByText(/Review Approval/i)).toBeInTheDocument()
  })

  it('shows Resume for paused', () => {
    useRunStore.setState({ activeRun: makeRun('paused') })
    render(<ActionBar />)
    expect(screen.getByRole('button', { name: /Resume/i })).toBeInTheDocument()
  })

  it('shows retry on failed and calls resumeRun', async () => {
    useRunStore.setState({ activeRun: makeRun('failed') })
    render(<ActionBar />)
    fireEvent.click(screen.getByRole('button', { name: '重试' }))
    await waitFor(() => expect(resumeRun).toHaveBeenCalledWith('r1'))
  })

  it('shows no retry on succeeded', () => {
    useRunStore.setState({ activeRun: makeRun('succeeded') })
    render(<ActionBar />)
    expect(screen.queryByRole('button', { name: '重试' })).toBeNull()
  })

  it('shows terminal summary for succeeded', () => {
    useRunStore.setState({ activeRun: makeRun('succeeded') })
    render(<ActionBar />)
    expect(screen.getByText(/Run succeeded/i)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Cancel/i })).toBeNull()
  })

  it('renders nothing when no active run', () => {
    const { container } = render(<ActionBar />)
    expect(container.firstChild).toBeNull()
  })
})
