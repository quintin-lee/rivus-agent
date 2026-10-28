import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { RunsHistoryDialog } from '@/components/RunsHistoryDialog'
import { api } from '@/lib/api'
import type { Run } from '@/lib/types'

vi.mock('@/lib/api', () => ({
  api: {
    listRuns: vi.fn(),
    getRun: vi.fn(),
    getEvents: vi.fn(),
    listApprovals: vi.fn(),
  },
}))

function run(over: Partial<Run> = {}): Run {
  return {
    id: 'r1abcdef',
    session_id: 's1abcdef',
    owner_id: 'o1',
    status: 'succeeded',
    mode: 'react',
    goal: '整理周报',
    budget: {},
    attempt: 1,
    created_at: 1700000000000,
    updated_at: 1700000000000,
    ...over,
  } as Run
}

describe('RunsHistoryDialog', () => {
  beforeEach(() => vi.mocked(api.listRuns).mockReset())

  it('lists owner-wide runs on open (no session filter)', async () => {
    vi.mocked(api.listRuns).mockResolvedValue([run(), run({ goal: '第二个目标', status: 'failed' })])
    render(<RunsHistoryDialog open onOpenChange={() => {}} />)
    await waitFor(() => expect(screen.getByText(/整理周报/)).toBeTruthy())
    expect(screen.getByText(/第二个目标/)).toBeTruthy()
    expect(api.listRuns).toHaveBeenCalledWith(undefined, 50, undefined)
  })

  it('shows empty state', async () => {
    vi.mocked(api.listRuns).mockResolvedValue([])
    render(<RunsHistoryDialog open onOpenChange={() => {}} />)
    await waitFor(() => expect(screen.getByText(/无运行记录/)).toBeTruthy())
  })

  it('shows error with retry', async () => {
    vi.mocked(api.listRuns).mockRejectedValueOnce(new Error('boom'))
    render(<RunsHistoryDialog open onOpenChange={() => {}} />)
    await waitFor(() => expect(screen.getByText(/boom|加载失败/)).toBeTruthy())
    vi.mocked(api.listRuns).mockResolvedValue([run()])
    fireEvent.click(screen.getByRole('button', { name: /重试/ }))
    await waitFor(() => expect(screen.getByText(/整理周报/)).toBeTruthy())
  })

  it('refetches with status when a filter chip is clicked', async () => {
    vi.mocked(api.listRuns).mockResolvedValue([run({ status: 'failed' })])
    render(<RunsHistoryDialog open onOpenChange={() => {}} />)
    await waitFor(() => expect(screen.getByText(/整理周报/)).toBeTruthy())
    fireEvent.click(screen.getByRole('button', { name: '失败' }))
    await waitFor(() => expect(api.listRuns).toHaveBeenCalledWith(undefined, 50, 'failed'))
  })

  it('opens the run detail dialog when a row is clicked', async () => {
    vi.mocked(api.listRuns).mockResolvedValue([run()])
    vi.mocked(api.getRun).mockResolvedValue({ run: run(), steps: [] })
    vi.mocked(api.getEvents).mockResolvedValue([])
    vi.mocked(api.listApprovals).mockResolvedValue([])
    render(<RunsHistoryDialog open onOpenChange={() => {}} />)
    await waitFor(() => expect(screen.getByText(/整理周报/)).toBeTruthy())

    fireEvent.click(screen.getByText(/整理周报/))
    await waitFor(() => expect(api.getRun).toHaveBeenCalledWith('r1abcdef'))
  })
})
