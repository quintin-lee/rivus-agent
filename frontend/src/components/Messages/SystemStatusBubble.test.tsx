import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { SystemStatusBubble } from './SystemStatusBubble'
import { api } from '@/lib/api'
import type { Run } from '@/lib/types'

vi.mock('@/lib/api', () => ({
  api: {
    cancelRun: vi.fn().mockResolvedValue({}),
    resumeRun: vi.fn().mockResolvedValue({}),
  },
}))

function run(status: Run['status']): Run {
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
  }
}

describe('SystemStatusBubble', () => {
  it('renders a cancel button for an active (running) run and calls api.cancelRun', async () => {
    const onRetryRun = vi.fn()
    render(<SystemStatusBubble run={run('running')} onRetryRun={onRetryRun} />)
    fireEvent.click(screen.getByRole('button', { name: /cancel/i }))
    const confirmBtn = await screen.findByRole('button', { name: /cancel run/i })
    fireEvent.click(confirmBtn)
    expect(api.cancelRun).toHaveBeenCalledWith('r1')
  })

  it('renders a retry button for a failed run and calls api.resumeRun + onRetryRun', async () => {
    const onRetryRun = vi.fn()
    render(<SystemStatusBubble run={run('failed')} onRetryRun={onRetryRun} />)
    expect(screen.getByRole('button', { name: /retry/i })).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: /retry/i }))
    await vi.waitFor(() => {
      expect(api.resumeRun).toHaveBeenCalledWith('r1')
      expect(onRetryRun).toHaveBeenCalledWith('r1')
    })
  })

  it('renders no action buttons for a succeeded run', () => {
    render(<SystemStatusBubble run={run('succeeded')} onRetryRun={vi.fn()} />)
    expect(screen.queryByRole('button', { name: /cancel/i })).toBeNull()
    expect(screen.queryByRole('button', { name: /retry/i })).toBeNull()
  })

  it('lists running steps with their descriptions', () => {
    render(
      <SystemStatusBubble
        run={run('running')}
        steps={[{ id: 'st1', run_id: 'r1', step_index: 1, description: 'step one', status: 'running' }]}
        onRetryRun={vi.fn()}
      />,
    )
    expect(screen.getByText('step one')).toBeTruthy()
  })

  it('renders a live progress row for an active run with progress', () => {
    const r = { ...run('running'), started_at: 1 }
    render(
      <SystemStatusBubble
        run={r}
        onRetryRun={vi.fn()}
        progress={{ toolCalls: 2, modelCalls: 3, seconds: 2 }}
      />,
    )
    expect(screen.getByText(/工具 2\/10/)).toBeTruthy()
    expect(screen.getByText(/模型 3\/10/)).toBeTruthy()
    expect(screen.getByText(/2s/)).toBeTruthy()
  })

  it('renders no progress row when progress is absent (terminal)', () => {
    render(<SystemStatusBubble run={run('succeeded')} onRetryRun={vi.fn()} />)
    expect(screen.queryByText(/工具 \d+\/\d+/)).toBeNull()
  })
})
