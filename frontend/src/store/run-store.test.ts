import { describe, it, expect, beforeEach, vi } from 'vitest'
import { useRunStore } from '@/store/run-store'
import { MAX_EVENTS } from '@/App.config'
import type { AgentEvent } from '@/lib/types'

vi.mock('@/lib/api', () => ({
  api: {
    getRun: vi.fn().mockResolvedValue({ run: {}, steps: [] }),
    listRuns: vi.fn().mockResolvedValue([]),
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

describe('run-store', () => {
  beforeEach(() => {
    useRunStore.setState({
      activeRunId: null,
      activeRun: null,
      activeSteps: [],
      events: [],
      eventFilter: 'all',
      runHistory: [],
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

  it('clearEvents empties events', () => {
    useRunStore.getState().appendEvent(mkEvent(1))
    useRunStore.getState().clearEvents()
    expect(useRunStore.getState().events).toEqual([])
  })

  it('setEventFilter updates filter', () => {
    useRunStore.getState().setEventFilter('tool.requested')
    expect(useRunStore.getState().eventFilter).toBe('tool.requested')
  })
})
