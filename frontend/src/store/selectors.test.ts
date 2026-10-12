import { describe, it, expect } from 'vitest'
import { groupEventsByRun } from '@/store/selectors'
import type { AgentEvent } from '@/lib/types'

function ev(runId: string, seq: number, type: AgentEvent['event_type'] = 'run.started'): AgentEvent {
  return {
    seq,
    run_id: runId,
    event_type: type,
    payload_json: '{}',
    sensitivity: 'normal',
    created_at: 0,
  }
}

describe('groupEventsByRun', () => {
  it('groups a flat event list by run_id preserving seq order', () => {
    const events = [ev('r1', 1), ev('r2', 1), ev('r1', 2), ev('r2', 2)]
    const out = groupEventsByRun(events)
    expect(out.r1?.map((e) => e.seq)).toEqual([1, 2])
    expect(out.r2?.map((e) => e.seq)).toEqual([1, 2])
  })

  it('returns an empty object for an empty list', () => {
    expect(groupEventsByRun([])).toEqual({})
  })

  it('does not mutate the input array', () => {
    const events = [ev('r1', 1), ev('r1', 2)]
    groupEventsByRun(events)
    expect(events).toEqual([ev('r1', 1), ev('r1', 2)])
  })
})
