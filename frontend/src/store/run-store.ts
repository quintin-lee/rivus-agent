import { create } from 'zustand'
import { api } from '@/lib/api'
import { MAX_EVENTS } from '@/App.config'
import type { AgentEvent, Budget, EventType, PendingGoal, Run, Step } from '@/lib/types'

export interface SubmitRunInput {
  sessionId: string
  goal: string
  mode?: 'react' | 'plan_execute'
  constraints?: string[]
  successCriteria?: string[]
  budget?: Partial<Budget>
}

interface RunState {
  activeRunId: string | null
  activeRun: Run | null
  activeSteps: Step[]
  events: AgentEvent[]
  eventFilter: EventType | 'all'
  runHistory: Run[]
  pendingGoals: PendingGoal[]
  cancelling: boolean
  cancelError: string | null
  activateRun: (id: string) => Promise<void>
  fetchRunAndSteps: (id: string) => Promise<void>
  appendEvent: (e: AgentEvent) => void
  setEventFilter: (f: EventType | 'all') => void
  clearEvents: () => void
  loadRunHistory: (sessionId: string) => Promise<void>
  submitRun: (input: SubmitRunInput) => Promise<void>
  dismissPendingGoal: (id: string) => void
  cancelActiveRun: (id: string) => Promise<void>
}

const TERMINAL_EVENTS: Partial<Record<EventType, boolean>> = {
  'run.finished': true,
  'run.failed': true,
  'run.cancelled': true,
  'run.timed_out': true,
}

const REFRESH_EVENTS: Partial<Record<EventType, boolean>> = {
  'run.resumed': true,
  'approval.decided': true,
  ...TERMINAL_EVENTS,
}

function pendingGoalId(): string {
  return `pending-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
}

export const useRunStore = create<RunState>((set, get) => ({
  activeRunId: null,
  activeRun: null,
  activeSteps: [],
  events: [],
  eventFilter: 'all',
  runHistory: [],
  pendingGoals: [],
  cancelling: false,
  cancelError: null,

  activateRun: async (id) => {
    set({ activeRunId: id, events: [], cancelling: false, cancelError: null })
    await get().fetchRunAndSteps(id)
  },

  fetchRunAndSteps: async (id) => {
    const { run, steps } = await api.getRun(id)
    set({ activeRun: run, activeSteps: steps })
  },

  appendEvent: (e) => {
    const cur = get().events
    if (cur.some((x) => x.seq === e.seq && x.event_type === e.event_type)) return
    const events = [...cur, e]
    if (events.length > MAX_EVENTS) {
      events.splice(0, events.length - MAX_EVENTS)
    }
    set({ events })
    if (REFRESH_EVENTS[e.event_type]) {
      const id = get().activeRunId
      if (id) void get().fetchRunAndSteps(id)
    }
  },

  setEventFilter: (f) => set({ eventFilter: f }),

  clearEvents: () => set({ events: [] }),

  loadRunHistory: async (sessionId) => {
    const runs = await api.listRuns(sessionId)
    set({ runHistory: runs, pendingGoals: [], cancelling: false, cancelError: null })
  },

  submitRun: async (input) => {
    const goalId = pendingGoalId()
    set((s) => ({
      pendingGoals: [
        ...s.pendingGoals,
        { id: goalId, goal: input.goal, status: 'pending' },
      ],
    }))
    try {
      const idempotencyKey = crypto.randomUUID()
      const res = await api.createRun(
        {
          session_id: input.sessionId,
          goal: input.goal,
          mode: input.mode,
          constraints: input.constraints,
          success_criteria: input.successCriteria,
          budget: input.budget,
        },
        idempotencyKey,
      )
      set({ pendingGoals: [] })
      await get().activateRun(res.run_id)
    } catch (e) {
      const msg = e instanceof Error ? e.message : '创建 Run 失败'
      set((s) => ({
        pendingGoals: s.pendingGoals.map((p) =>
          p.id === goalId ? { ...p, status: 'failed', error: msg } : p,
        ),
      }))
    }
  },

  dismissPendingGoal: (id) =>
    set((s) => ({ pendingGoals: s.pendingGoals.filter((p) => p.id !== id) })),

  cancelActiveRun: async (id) => {
    set({ cancelling: true, cancelError: null })
    try {
      await api.cancelRun(id)
      set({ cancelling: false, cancelError: null })
    } catch (e) {
      set({ cancelling: false, cancelError: e instanceof Error ? e.message : 'Stop failed' })
    }
  },
}))
