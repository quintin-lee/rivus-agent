import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { SubagentCard } from '@/components/Messages/SubagentCard'
import { api } from '@/lib/api'
import type { Run } from '@/lib/types'

vi.mock('@/lib/api', () => ({ api: { listRuns: vi.fn() } }))

function childRun(goal: string, i: number, over: Partial<Run> = {}): Run {
  return {
    id: `c${i}`,
    session_id: 's1',
    owner_id: 'o1',
    status: 'succeeded',
    mode: 'react',
    goal,
    budget: {
      max_duration_seconds: 0,
      max_model_calls: 0,
      max_tool_calls: 0,
      max_iterations: 0,
      max_output_bytes: 0,
    },
    attempt: 1,
    created_at: 1000 + i,
    updated_at: 2000 + i,
    parent_run_id: 'r1',
    ...over,
  }
}

const parent = {
  id: 'r1',
  session_id: 's1',
  status: 'succeeded',
} as Run

function msg(count = 1) {
  return { kind: 'system-subagent' as const, id: 'x', runId: 'r1', run: parent, count }
}

describe('SubagentCard', () => {
  beforeEach(() => vi.mocked(api.listRuns).mockReset())

  it('collapsed shows count and expands to fetch children', async () => {
    vi.mocked(api.listRuns).mockResolvedValue([childRun('g1', 1), childRun('g2', 2)])
    render(<SubagentCard message={msg(2)} />)
    expect(screen.getByText(/子 agent ×2/)).toBeTruthy()
    fireEvent.click(screen.getByRole('button'))
    await waitFor(() => expect(screen.getByText(/g1/)).toBeTruthy())
    expect(screen.getByText(/g2/)).toBeTruthy()
    expect(vi.mocked(api.listRuns)).toHaveBeenCalledWith('s1', 200)
  })

  it('shows failed error_code and duration', async () => {
    vi.mocked(api.listRuns).mockResolvedValue([
      childRun('bad', 1, {
        status: 'failed',
        error_code: 'model_failed',
        created_at: 1000,
        updated_at: 3500,
      }),
    ])
    render(<SubagentCard message={msg()} />)
    fireEvent.click(screen.getByRole('button'))
    await waitFor(() => expect(screen.getByText(/model_failed/)).toBeTruthy())
    expect(screen.getByText(/2s/)).toBeTruthy() // (3500-1000)/1000
  })

  it('empty children state', async () => {
    vi.mocked(api.listRuns).mockResolvedValue([])
    render(<SubagentCard message={msg()} />)
    fireEvent.click(screen.getByRole('button'))
    await waitFor(() => expect(screen.getByText(/无子 run/)).toBeTruthy())
  })

  it('error + retry', async () => {
    vi.mocked(api.listRuns).mockRejectedValueOnce(new Error('boom'))
    render(<SubagentCard message={msg()} />)
    fireEvent.click(screen.getByRole('button'))
    await waitFor(() => expect(screen.getByText(/boom|加载失败/)).toBeTruthy())
    vi.mocked(api.listRuns).mockResolvedValue([childRun('g1', 1)])
    fireEvent.click(screen.getByRole('button', { name: /重试/ }))
    await waitFor(() => expect(screen.getByText(/g1/)).toBeTruthy())
  })
})
