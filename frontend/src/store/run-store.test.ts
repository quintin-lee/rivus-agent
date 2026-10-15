import { describe, it, expect, beforeEach, vi } from 'vitest'
import { useRunStore } from '@/store/run-store'
import { MAX_EVENTS } from '@/App.config'
import { api } from '@/lib/api'
import type { AgentEvent } from '@/lib/types'

vi.mock('@/lib/api', () => ({
  api: {
    getRun: vi.fn().mockResolvedValue({ run: {}, steps: [] }),
    listRuns: vi.fn().mockResolvedValue([]),
    createRun: vi.fn().mockResolvedValue({ run_id: 'r1', status: 'queued', duplicated: false }),
    cancelRun: vi.fn().mockResolvedValue(undefined),
  },
}))

function mkEvent(seq: number): AgentEvent {
  return {
    seq,
    run_id: 'r1',
    event_type: 'run.started',
    payload_json: '{}',
    sensitivity: 'normal',
    created_at: 0,
  }
}

// The submitRun tests permanently clobber activateRun in store state via
// setState({ activateRun: mockFn }); capture the real action once so tests
// that need it can restore it.
const realActivateRun = useRunStore.getState().activateRun

describe('run-store', () => {
  beforeEach(() => {
    useRunStore.setState({
      activeRunId: null,
      activeRun: null,
      activeSteps: [],
      events: [],
      eventFilter: 'all',
      runHistory: [],
      pendingGoals: [],
      cancelling: false,
      cancelError: null,
      activateRun: realActivateRun,
    })
  })

  it('appendEvent keeps at most MAX_EVENTS (drops oldest)', () => {
    for (let i = 0; i < MAX_EVENTS + 10; i++) {
      useRunStore.getState().appendEvent(mkEvent(i))
    }
    const evs = useRunStore.getState().events
    expect(evs.length).toBe(MAX_EVENTS)
    expect(evs[0].seq).toBe(10)
    expect(evs[evs.length - 1].seq).toBe(MAX_EVENTS + 9)
  })

  it('appendEvent dedupes by (seq, event_type)', () => {
    useRunStore.getState().appendEvent(mkEvent(1))
    useRunStore.getState().appendEvent(mkEvent(1))
    expect(useRunStore.getState().events.length).toBe(1)
  })

  it('clearEvents empties events', () => {
    useRunStore.getState().appendEvent(mkEvent(1))
    useRunStore.getState().clearEvents()
    expect(useRunStore.getState().events).toEqual([])
  })

  it('setEventFilter updates filter', () => {
    useRunStore.getState().setEventFilter('tool.requested')
    expect(useRunStore.getState().eventFilter).toBe('tool.requested')
  })

  it('submitRun: optimistic pending goal, clears on success, activates run', async () => {
    vi.mocked(api.createRun).mockResolvedValue({ run_id: 'r9', status: 'queued', duplicated: false } as never)
    const activateRun = vi.fn().mockResolvedValue(undefined)
    useRunStore.setState({ activateRun })

    useRunStore.getState().submitRun({
      sessionId: 's1',
      goal: 'do thing',
      mode: 'react',
      constraints: undefined,
      successCriteria: undefined,
      budget: undefined,
    })

    const pending = useRunStore.getState().pendingGoals
    expect(pending).toHaveLength(1)
    expect(pending[0]).toMatchObject({ goal: 'do thing', status: 'pending' })

    await vi.waitFor(() => {
      expect(useRunStore.getState().pendingGoals).toHaveLength(0)
    })
    expect(vi.mocked(api.createRun)).toHaveBeenCalledWith(
      expect.objectContaining({ session_id: 's1', goal: 'do thing' }),
      expect.any(String),
    )
    expect(activateRun).toHaveBeenCalledWith('r9')
  })

  it('submitRun: on failure, keeps goal with failed status + error, does not activate', async () => {
    vi.mocked(api.createRun).mockRejectedValue(new Error('network down'))
    const activateRun = vi.fn().mockResolvedValue(undefined)
    useRunStore.setState({ activateRun })

    const promise = useRunStore.getState().submitRun({
      sessionId: 's1',
      goal: 'do thing',
      mode: 'react',
      constraints: undefined,
      successCriteria: undefined,
      budget: undefined,
    })

    await vi.waitFor(() => {
      const p = useRunStore.getState().pendingGoals[0]
      expect(p?.status).toBe('failed')
    })
    await promise.catch(() => {})
    expect(useRunStore.getState().pendingGoals[0]).toMatchObject({
      goal: 'do thing',
      status: 'failed',
      error: 'network down',
    })
    expect(activateRun).not.toHaveBeenCalled()
  })

  it('submitRun: clears pendingGoals after a successful submit of a later goal', async () => {
    const activateRun = vi.fn().mockResolvedValue(undefined)
    useRunStore.setState({ activateRun })

    useRunStore.setState({
      pendingGoals: [{ id: 'p-old', goal: 'stale', status: 'failed', error: 'x' }],
    })
    expect(useRunStore.getState().pendingGoals).toHaveLength(1)

    vi.mocked(api.createRun).mockResolvedValue({ run_id: 'r10', status: 'queued', duplicated: false } as never)
    await useRunStore.getState().submitRun({
      sessionId: 's1',
      goal: 'retry works',
      mode: 'react',
      constraints: undefined,
      successCriteria: undefined,
      budget: undefined,
    })
    // After successful submit, all pendingGoals are cleared (the stale failed one is dropped too)
    expect(useRunStore.getState().pendingGoals).toHaveLength(0)
  })

  it('cancelActiveRun: calls api.cancelRun and clears cancelling/cancelError on success', async () => {
    useRunStore.setState({ activeRunId: 'r1', cancelling: true, cancelError: 'old' })
    await useRunStore.getState().cancelActiveRun('r1')
    expect(vi.mocked(api.cancelRun)).toHaveBeenCalledWith('r1')
    expect(useRunStore.getState().cancelling).toBe(false)
    expect(useRunStore.getState().cancelError).toBeNull()
  })

  it('cancelActiveRun: stores the error message and re-enables on failure', async () => {
    vi.mocked(api.cancelRun).mockRejectedValueOnce(new Error('stop failed'))
    useRunStore.setState({ activeRunId: 'r1' })
    await useRunStore.getState().cancelActiveRun('r1')
    expect(useRunStore.getState().cancelling).toBe(false)
    expect(useRunStore.getState().cancelError).toBe('stop failed')
  })

  it('activateRun resets cancelling and cancelError', async () => {
    vi.mocked(api.getRun).mockResolvedValueOnce({ run: {}, steps: [] } as never)
    useRunStore.setState({ cancelling: true, cancelError: 'old' })
    await useRunStore.getState().activateRun('r2')
    expect(useRunStore.getState().cancelling).toBe(false)
    expect(useRunStore.getState().cancelError).toBeNull()
  })
})
