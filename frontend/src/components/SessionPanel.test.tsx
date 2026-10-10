import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { SessionPanel } from './SessionPanel'
import { useSessionStore } from '@/store/session-store'
import { useRunStore } from '@/store/run-store'

const { listRuns, deleteSession } = vi.hoisted(() => ({
  listRuns: vi.fn().mockResolvedValue([]),
  deleteSession: vi.fn().mockResolvedValue(undefined),
}))

vi.mock('@/lib/api', () => ({ api: { listRuns, deleteSession }, ApiError: class extends Error {} }))

function seed() {
  useSessionStore.setState({
    sessions: [
      { session_id: 's1', title: 'one', created_at: 1, updated_at: 1 },
      { session_id: 's2', title: 'two', created_at: 2, updated_at: 2 },
    ],
    activeSessionId: 's1',
  })
  useRunStore.setState({
    activeRunId: null,
    activeRun: null,
    activeSteps: [],
    events: [],
    runHistory: [],
  })
}

describe('SessionPanel delete', () => {
  beforeEach(() => {
    listRuns.mockClear()
    deleteSession.mockClear()
    listRuns.mockResolvedValue([])
    seed()
  })

  it('confirms and deletes, switching active session', async () => {
    render(<SessionPanel />)
    fireEvent.click(screen.getAllByTitle('删除会话')[0])
    await waitFor(() => expect(listRuns).toHaveBeenCalledWith('s1'))
    expect(screen.getByText(/0 个 Run/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '删除' }))
    await waitFor(() => expect(deleteSession).toHaveBeenCalledWith('s1'))
    expect(useSessionStore.getState().activeSessionId).toBe('s2')
    expect(useSessionStore.getState().sessions).toHaveLength(1)
  })

  it('disables confirm when active runs exist', async () => {
    listRuns.mockResolvedValue([
      { id: 'r1', session_id: 's1', owner_id: 'u', status: 'running', mode: 'react', goal: 'g', budget: {}, attempt: 1, created_at: 1, updated_at: 1 },
    ])
    render(<SessionPanel />)
    fireEvent.click(screen.getAllByTitle('删除会话')[0])
    await screen.findByText(/先取消/)
    expect(screen.getByRole('button', { name: '删除' })).toBeDisabled()
    expect(deleteSession).not.toHaveBeenCalled()
  })
})
