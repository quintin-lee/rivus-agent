import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useSessionStore } from '@/store/session-store'
import { useRunStore } from '@/store/run-store'
import type { Session } from '@/lib/types'

const { createSession, listSessions, deleteSession } = vi.hoisted(() => ({
  createSession: vi.fn().mockResolvedValue({ session_id: 's-new' }),
  listSessions: vi.fn().mockResolvedValue([]),
  deleteSession: vi.fn().mockResolvedValue(undefined),
}))

vi.mock('@/lib/api', () => ({ api: { createSession, listSessions, deleteSession } }))

function mkSession(id: string, title: string): Session {
  return { session_id: id, title, created_at: 0, updated_at: 0 }
}

describe('session-store', () => {
  beforeEach(() => {
    useSessionStore.setState({ sessions: [], activeSessionId: null })
    createSession.mockClear()
    listSessions.mockClear()
  })

  it('createSession adds the session and sets it active', async () => {
    await useSessionStore.getState().createSession('demo')
    expect(createSession).toHaveBeenCalledWith('demo')
    const s = useSessionStore.getState()
    expect(s.activeSessionId).toBe('s-new')
    expect(s.sessions).toHaveLength(1)
    expect(s.sessions[0].title).toBe('demo')
  })

  it('switchSession sets activeSessionId', () => {
    useSessionStore.setState({ sessions: [mkSession('a', 'A'), mkSession('b', 'B')] })
    useSessionStore.getState().switchSession('b')
    expect(useSessionStore.getState().activeSessionId).toBe('b')
  })

  it('loadSessions keeps existing activeSessionId when set', async () => {
    useSessionStore.setState({ activeSessionId: 'keep' })
    listSessions.mockResolvedValueOnce([mkSession('x', 'X'), mkSession('y', 'Y')])
    await useSessionStore.getState().loadSessions()
    expect(useSessionStore.getState().activeSessionId).toBe('keep')
    expect(useSessionStore.getState().sessions).toHaveLength(2)
  })

  it('loadSessions falls back to first session when none active', async () => {
    useSessionStore.setState({ activeSessionId: null })
    listSessions.mockResolvedValueOnce([mkSession('x', 'X'), mkSession('y', 'Y')])
    await useSessionStore.getState().loadSessions()
    expect(useSessionStore.getState().activeSessionId).toBe('x')
  })

  it('loadSessions leaves activeSessionId null when list is empty', async () => {
    useSessionStore.setState({ activeSessionId: null })
    listSessions.mockResolvedValueOnce([])
    await useSessionStore.getState().loadSessions()
    expect(useSessionStore.getState().activeSessionId).toBeNull()
    expect(useSessionStore.getState().sessions).toEqual([])
  })

  it('deleteSession removes the session and keeps a different active session', async () => {
    useSessionStore.setState({
      sessions: [mkSession('a', 'A'), mkSession('b', 'B')],
      activeSessionId: 'b',
    })
    await useSessionStore.getState().deleteSession('a')
    expect(deleteSession).toHaveBeenCalledWith('a')
    const s = useSessionStore.getState()
    expect(s.sessions).toHaveLength(1)
    expect(s.sessions[0].session_id).toBe('b')
    expect(s.activeSessionId).toBe('b')
  })

  it('deleteSession clears run-history when the deleted session was active', async () => {
    useSessionStore.setState({
      sessions: [mkSession('a', 'A'), mkSession('b', 'B')],
      activeSessionId: 'a',
    })
    useRunStore.setState({
      activeRunId: 'run-1',
      activeRun: { id: 'run-1' } as never,
      activeSteps: [],
      events: [],
      runHistory: [{ id: 'run-1' } as never],
    })
    await useSessionStore.getState().deleteSession('a')
    const run = useRunStore.getState()
    expect(run.activeRunId).toBeNull()
    expect(run.activeRun).toBeNull()
    expect(run.activeSteps).toEqual([])
    expect(run.events).toEqual([])
    expect(run.runHistory).toEqual([])
  })
})
