import { describe, it, expect } from 'vitest'
import { deriveMessages, type ChatMessage } from '@/lib/chat-messages'
import type { Run, AgentEvent, Step } from '@/lib/types'

function mkRun(id: string, status: Run['status'], extra: Partial<Run> = {}): Run {
  return {
    id,
    session_id: 's1',
    owner_id: 'o1',
    status,
    mode: 'react',
    goal: `goal-${id}`,
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
    ...extra,
  }
}

function mkStep(overrides: Partial<Step> = {}): Step {
  return {
    id: 'st1',
    run_id: 'r1',
    step_index: 1,
    description: 'd',
    status: 'running',
    ...overrides,
  }
}

function mkEv(seq: number, type: AgentEvent['event_type'], payload = '{}'): AgentEvent {
  return {
    seq,
    run_id: 'r1',
    event_type: type,
    payload_json: payload,
    sensitivity: 'normal',
    created_at: 0,
  }
}

describe('deriveMessages', () => {
  it('returns empty array when no history, no pending goals', () => {
    const out = deriveMessages({
      runHistory: [],
      activeRun: null,
      activeSteps: [],
      eventsByRun: {},
      pendingGoals: [],
      now: 0,
    })
    expect(out).toEqual([])
  })

  it('emits user + system-status bubbles for a terminal run', () => {
    const run = mkRun('r1', 'succeeded', { result_json: '{"ok":true}' })
    const out = deriveMessages({
      runHistory: [run],
      activeRun: run,
      activeSteps: [],
      eventsByRun: {},
      pendingGoals: [],
      now: 0,
    })
    const kinds = out.map((m) => m.kind)
    expect(kinds).toContain('user')
    expect(kinds).toContain('system-status')
    expect(kinds).toContain('assistant')
  })

  it('does not emit assistant bubble for non-terminal run', () => {
    const run = mkRun('r1', 'running')
    const out = deriveMessages({
      runHistory: [run],
      activeRun: run,
      activeSteps: [mkStep()],
      eventsByRun: {},
      pendingGoals: [],
      now: 0,
    })
    expect(out.map((m) => m.kind)).not.toContain('assistant')
  })

  it('emits pending user bubble with pending status from pendingGoals', () => {
    const out = deriveMessages({
      runHistory: [],
      activeRun: null,
      activeSteps: [],
      eventsByRun: {},
      pendingGoals: [{ id: 'p1', goal: 'g', status: 'pending' }],
      now: 0,
    })
    expect(out).toHaveLength(1)
    expect(out[0]).toMatchObject({ kind: 'user', goal: 'g', status: 'pending' })
  })

  it('emits failed user bubble preserving goal text and error', () => {
    const out = deriveMessages({
      runHistory: [],
      activeRun: null,
      activeSteps: [],
      eventsByRun: {},
      pendingGoals: [{ id: 'p1', goal: 'g', status: 'failed', error: 'boom' }],
      now: 0,
    })
    expect(out[0]).toMatchObject({ kind: 'user', goal: 'g', status: 'failed', error: 'boom' })
  })

  it('orders multiple runs by created_at ascending', () => {
    const r1 = mkRun('r1', 'succeeded', { created_at: 1 })
    const r2 = mkRun('r2', 'succeeded', { created_at: 2 })
    const out = deriveMessages({
      runHistory: [r2, r1], // intentionally reversed input
      activeRun: null,
      activeSteps: [],
      eventsByRun: {},
      pendingGoals: [],
      now: 0,
    })
    const userGoals = out.filter((m): m is Extract<ChatMessage, { kind: 'user' }> => m.kind === 'user')
    expect(userGoals.map((m) => m.runId)).toEqual(['r1', 'r2'])
  })

  it('emits system-approval bubble for approval.requested events, ordered by seq, merging multiple approvals per run', () => {
    const run = mkRun('r1', 'waiting_approval')
    const out = deriveMessages({
      runHistory: [run],
      activeRun: run,
      activeSteps: [],
      eventsByRun: {
        r1: [
          mkEv(2, 'approval.requested', '{"approval_id":"a1","tool_name":"t1"}'),
          mkEv(1, 'approval.requested', '{"approval_id":"a0","tool_name":"t0"}'), // out of order input
        ],
      },
      pendingGoals: [],
      now: 0,
    })
    const approvals = out.filter((m) => m.kind === 'system-approval')
    expect(approvals).toHaveLength(2)
    // a0 (seq 1) must come before a1 (seq 2) in the output
    const first = approvals[0] as { state: string; event: AgentEvent }
    expect(JSON.parse(first.event.payload_json).approval_id).toBe('a0')
    expect(first.state).toBe('pending')
  })

  it('marks approval as approved when a later approval.decided event exists', () => {
    const run = mkRun('r1', 'waiting_approval')
    const out = deriveMessages({
      runHistory: [run],
      activeRun: run,
      activeSteps: [],
      eventsByRun: {
        r1: [
          mkEv(1, 'approval.requested', '{"approval_id":"a1","tool_name":"t1"}'),
          mkEv(2, 'approval.decided', '{"approval_id":"a1","approved":true}'),
        ],
      },
      pendingGoals: [],
      now: 0,
    })
    const approval = out.find((m) => m.kind === 'system-approval') as
      | { state: string }
      | undefined
    expect(approval?.state).toBe('approved')
    // only one bubble, not two
    expect(out.filter((m) => m.kind === 'system-approval')).toHaveLength(1)
  })

  it('marks approval as rejected when approval.decided has approved=false', () => {
    const run = mkRun('r1', 'waiting_approval')
    const out = deriveMessages({
      runHistory: [run],
      activeRun: run,
      activeSteps: [],
      eventsByRun: {
        r1: [
          mkEv(1, 'approval.requested', '{"approval_id":"a1","tool_name":"t1"}'),
          mkEv(2, 'approval.decided', '{"approval_id":"a1","approved":false,"reason":"nope"}'),
        ],
      },
      pendingGoals: [],
      now: 0,
    })
    const approval = out.find((m) => m.kind === 'system-approval') as
      | { state: string }
      | undefined
    expect(approval?.state).toBe('rejected')
  })

  it('injects activeSteps and activeRun budget into the active run system-status bubble', () => {
    const run = mkRun('r1', 'running')
    const steps = [mkStep()]
    const out = deriveMessages({
      runHistory: [run],
      activeRun: run,
      activeSteps: steps,
      eventsByRun: {},
      pendingGoals: [],
      now: 0,
    })
    const status = out.find((m) => m.kind === 'system-status') as
      | { steps: { id: string }[]; budget: { max_model_calls: number } }
      | undefined
    expect(status?.steps).toEqual(steps)
    expect(status?.budget.max_model_calls).toBe(10)
  })

  it('does not double-emit activeRun when it also appears in runHistory', () => {
    const run = mkRun('r1', 'succeeded', { result_json: '{"ok":true}' })
    const out = deriveMessages({
      runHistory: [run],
      activeRun: run,
      activeSteps: [],
      eventsByRun: {},
      pendingGoals: [],
      now: 0,
    })
    const statusCount = out.filter((m) => m.kind === 'system-status').length
    expect(statusCount).toBe(1)
  })

  it('derives expired when an approval was never decided and its run is terminal', () => {
    const run = mkRun('r1', 'failed')
    const out = deriveMessages({
      runHistory: [run], activeRun: run, activeSteps: [],
      eventsByRun: { r1: [mkEv(1, 'approval.requested', '{"approval_id":"a1","tool_name":"t1"}')] },
      pendingGoals: [],
      now: 0,
    })
    const a = out.find((m) => m.kind === 'system-approval') as { state: string } | undefined
    expect(a?.state).toBe('expired')
  })

  it('keeps pending when the approval is undecided but the run is not terminal', () => {
    const run = mkRun('r1', 'waiting_approval')
    const out = deriveMessages({
      runHistory: [run], activeRun: run, activeSteps: [],
      eventsByRun: { r1: [mkEv(1, 'approval.requested', '{"approval_id":"a1","tool_name":"t1"}')] },
      pendingGoals: [],
      now: 0,
    })
    const a = out.find((m) => m.kind === 'system-approval') as { state: string } | undefined
    expect(a?.state).toBe('pending')
  })

  it('derives no approval messages for real-data payloads (no approval_id key)', () => {
    const run = mkRun('r1', 'waiting_approval')
    const out = deriveMessages({
      runHistory: [run], activeRun: run, activeSteps: [],
      eventsByRun: { r1: [mkEv(1, 'approval.requested', '{"text":"shell requires approval"}')] },
      pendingGoals: [],
      now: 0,
    })
    expect(out.filter((m) => m.kind === 'system-approval')).toHaveLength(0)
  })

  it('derives progress (tool/model counts + elapsed) for an active run', () => {
    const startMs = 1_000_000
    const now = startMs + 2000
    const run = mkRun('r1', 'running', { started_at: startMs })
    const evs = [
      mkEv(1, 'tool.completed'),
      mkEv(2, 'tool.completed'),
      mkEv(3, 'model.completed'),
      mkEv(4, 'model.completed'),
      mkEv(5, 'model.completed'),
    ]
    const out = deriveMessages({ runHistory: [], activeRun: run, activeSteps: [], eventsByRun: { r1: evs }, pendingGoals: [], now })
    const status = out.find((m) => m.kind === 'system-status')
    expect(status).toMatchObject({ progress: { toolCalls: 2, modelCalls: 3, seconds: 2 } })
  })

  it('does not derive progress for a terminal run', () => {
    const run = mkRun('r1', 'succeeded', { started_at: 1 })
    const out = deriveMessages({ runHistory: [run], activeRun: null, activeSteps: [], eventsByRun: { r1: [mkEv(1, 'tool.completed')] }, pendingGoals: [], now: 1000 })
    const status = out.find((m) => m.kind === 'system-status') as Extract<ChatMessage, { kind: 'system-status' }> | undefined
    expect(status?.progress).toBeUndefined()
  })

  it('falls back to created_at when started_at is missing (active run)', () => {
    const run = mkRun('r1', 'running') // created_at:1, no started_at
    const out = deriveMessages({ runHistory: [], activeRun: run, activeSteps: [], eventsByRun: { r1: [mkEv(1, 'tool.completed')] }, pendingGoals: [], now: 5001 })
    const status = out.find((m) => m.kind === 'system-status') as Extract<ChatMessage, { kind: 'system-status' }> | undefined
    expect(status?.progress).toMatchObject({ toolCalls: 1, seconds: 5 })
  })
})
