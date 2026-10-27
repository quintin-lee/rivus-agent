import type { AgentEvent, Budget, PendingGoal, Run, Step } from './types'
import { TERMINAL_RUN_STATUSES, isActiveRunStatus, eventPayload } from './status'

export type ChatMessage =
  | {
      kind: 'user'
      id: string
      runId: string
      goal: string
      status: 'pending' | 'sent' | 'failed'
      error?: string
    }
  | {
      kind: 'assistant'
      id: string
      runId: string
      run: Run
      resultJson?: string
      error?: { code: string; summary?: string }
    }
  | {
      kind: 'system-status'
      id: string
      runId: string
      run: Run
      status: Run['status']
      mode: string
      goal: string
      steps: Step[]
      budget: Budget
      progress?: { toolCalls: number; modelCalls: number; seconds: number }
      partialText?: string // live streaming partial from the latest model.token
      error?: { code: string; summary?: string }
    }
  | {
      kind: 'system-events'
      id: string
      runId: string
      events: AgentEvent[]
      collapsed: boolean
    }
  | {
      kind: 'system-subagent'
      id: string
      runId: string
      run: Run
      count: number
    }
  | {
      kind: 'system-tools'
      id: string
      runId: string
      calls: { tool: string; args?: string; output?: string; seq: number }[]
    }
  | {
      kind: 'system-approval'
      id: string
      runId: string
      event: AgentEvent
      state: 'pending' | 'approved' | 'rejected' | 'expired'
    }

export interface DeriveInput {
  runHistory: Run[]
  activeRun: Run | null
  activeSteps: Step[]
  eventsByRun: Record<string, AgentEvent[]>
  pendingGoals: PendingGoal[]
  now: number
}

/**
 * Derive the chat message list from all existing store state. Pure function,
 * no side effects, no API calls. Order: pending goals first (they are the
 * newest, not-yet-committed turns), then runs by created_at ascending.
 */
export function deriveMessages(input: DeriveInput): ChatMessage[] {
  const { runHistory, activeRun, activeSteps, eventsByRun, pendingGoals, now } = input

  const messages: ChatMessage[] = []

  for (const p of pendingGoals) {
    messages.push({
      kind: 'user',
      id: p.id,
      runId: p.id,
      goal: p.goal,
      status: p.status,
      error: p.error,
    })
  }

  // De-dupe activeRun against runHistory (it will usually already be in runHistory
  // after fetchRunAndSteps refresh, but keep it explicit so we never emit two
  // status bubbles for the same run id).
  const seen = new Set<string>()
  const ordered = [...runHistory].sort((a, b) => a.created_at - b.created_at)
  for (const run of ordered) {
    if (run.id === activeRun?.id) continue
    seen.add(run.id)
    emitRun(messages, run, activeSteps, eventsByRun, now)
  }
  if (activeRun && !seen.has(activeRun.id)) {
    emitRun(messages, activeRun, activeSteps, eventsByRun, now)
  }

  return messages
}

function emitRun(
  messages: ChatMessage[],
  run: Run,
  steps: Step[],
  eventsByRun: Record<string, AgentEvent[]>,
  now: number,
) {
  const userMsg: ChatMessage = {
    kind: 'user',
    id: `user-${run.id}`,
    runId: run.id,
    goal: run.goal,
    status: 'sent',
  }
  messages.push(userMsg)
  let progress: { toolCalls: number; modelCalls: number; seconds: number } | undefined
  let partialText: string | undefined
  if (isActiveRunStatus(run.status)) {
    const evs = eventsByRun[run.id] ?? []
    let toolCalls = evs.filter((e) => e.event_type === 'tool.completed').length
    let modelCalls = evs.filter((e) => e.event_type === 'model.completed').length
    const startMs = run.started_at ?? run.created_at
    const seconds = startMs > 0 ? Math.max(0, Math.round((now - startMs) / 1000)) : 0
    // 优先:最新一条 budget.consumed(后端单-attempt 权威累计)。MAX_EVENTS 头部
    // 截断丢最旧,最新那条恒存活 → 截断免疫;缺失(旧数据)则保持 .completed 回退。
    const consumed = evs.filter((e) => e.event_type === 'budget.consumed')
    if (consumed.length > 0) {
      const p = eventPayload(consumed[consumed.length - 1])
      const mc = Number(p.model_calls)
      const tc = Number(p.tool_calls)
      if (Number.isFinite(mc)) modelCalls = mc
      if (Number.isFinite(tc)) toolCalls = tc
    }
    progress = { toolCalls, modelCalls, seconds }
    // Live partial from the latest model.token. SSE (run-store appendEvent)
    // lands ALL model.token rows into eventsByRun (groupEventsByRun buckets
    // by run_id, no event_type filter). payload_json is a string → parse via
    // eventPayload; the persistence bridge stores {"text": "...", "tool": ""}.
    // Take the LAST model.token → its text IS the current partial
    // (full-so-far, idempotent). On terminal, the isActiveRunStatus guard is
    // false → partialText stays undefined.
    const tokens = evs.filter((e) => e.event_type === 'model.token')
    if (tokens.length > 0) {
      const text = eventPayload(tokens[tokens.length - 1]).text
      partialText = typeof text === 'string' ? text : undefined
    }
  }

  const statusMsg: ChatMessage = {
    kind: 'system-status',
    id: `status-${run.id}`,
    runId: run.id,
    run,
    status: run.status,
    mode: run.mode,
    goal: run.goal,
    steps,
    budget: run.budget,
    error: run.error_code
      ? { code: run.error_code, summary: run.error_summary }
      : undefined,
    progress,
    partialText,
  }
  messages.push(statusMsg)

  const events = eventsByRun[run.id] ?? []

  let subagentCount = 0
  for (const e of events) {
    if (e.event_type !== 'tool.completed') continue
    const t = eventPayload(e).tool
    if (t === 'task' || t === 'tasks') subagentCount++
  }
  if (subagentCount > 0) {
    messages.push({
      kind: 'system-subagent',
      id: `subagent-${run.id}`,
      runId: run.id,
      run,
      count: subagentCount,
    })
  }

  const toolCalls: { tool: string; args?: string; output?: string; seq: number }[] = []
  for (const e of events) {
    if (e.event_type !== 'tool.completed') continue
    const p = eventPayload(e)
    const args = typeof p.args === 'string' ? p.args : undefined
    const output = typeof p.output === 'string' ? p.output : undefined
    if (args === undefined && output === undefined) continue
    toolCalls.push({ tool: typeof p.tool === 'string' ? p.tool : '', args, output, seq: e.seq })
  }
  if (toolCalls.length > 0) {
    messages.push({ kind: 'system-tools', id: `tools-${run.id}`, runId: run.id, calls: toolCalls })
  }

  if (events.length > 0) {
    messages.push({
      kind: 'system-events',
      id: `events-${run.id}`,
      runId: run.id,
      events,
      collapsed: true,
    })
  }

  const approvalMsgs = deriveApprovals(run.id, events, TERMINAL_RUN_STATUSES.includes(run.status))
  messages.push(...approvalMsgs)

  if (TERMINAL_RUN_STATUSES.includes(run.status)) {
    messages.push({
      kind: 'assistant',
      id: `assistant-${run.id}`,
      runId: run.id,
      run,
      resultJson: run.result_json,
      error: run.error_code
        ? { code: run.error_code, summary: run.error_summary }
        : undefined,
    })
  }
}

function deriveApprovals(
  runId: string,
  events: AgentEvent[],
  runTerminal: boolean,
): ChatMessage[] {
  const sorted = [...events].sort((a, b) => a.seq - b.seq)
  const requested = new Map<string, AgentEvent>()
  const decided = new Map<string, { approved: boolean; reason?: string }>()
  for (const e of sorted) {
    if (e.event_type === 'approval.requested') {
      const p = parseApprovalPayload(e)
      if (p.approvalId) requested.set(p.approvalId, e)
    } else if (e.event_type === 'approval.decided') {
      const p = parseApprovalPayload(e)
      if (p.approvalId) decided.set(p.approvalId, { approved: p.approved, reason: p.reason })
    }
  }

  const out: ChatMessage[] = []
  for (const [approvalId, event] of requested) {
    const d = decided.get(approvalId)
    const state: 'pending' | 'approved' | 'rejected' | 'expired' = d
      ? (d.approved ? 'approved' : 'rejected')
      : runTerminal ? 'expired' : 'pending'
    out.push({
      kind: 'system-approval',
      id: `approval-${runId}-${approvalId}`,
      runId,
      event,
      state,
    })
  }
  out.sort((a, b) => {
    // All entries in `out` have kind 'system-approval' (narrowed at construction)
    return (a as Extract<ChatMessage, { kind: 'system-approval' }>).event.seq
      - (b as Extract<ChatMessage, { kind: 'system-approval' }>).event.seq
  })
  return out
}

function parseApprovalPayload(e: AgentEvent) {
  let approvalId = ''
  let approved = false
  let reason: string | undefined
  try {
    const p = JSON.parse(e.payload_json || '{}')
    approvalId = p.approval_id ?? p.approvalId ?? ''
    if (p.approved === true) approved = true
    reason = p.reason
  } catch {
    // leave defaults
  }
  return { approvalId, approved, reason }
}
