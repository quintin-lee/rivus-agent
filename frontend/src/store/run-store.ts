import { create } from 'zustand'
import { api } from '@/lib/api'
import { MAX_EVENTS } from '@/App.config'
import type { AgentEvent, EventType, Run, Step } from '@/lib/types'

interface RunState {
  activeRunId: string | null
  activeRun: Run | null
  activeSteps: Step[]
  events: AgentEvent[]
  eventFilter: EventType | 'all'
  runHistory: Run[]
  activateRun: (id: string) => Promise<void>
  fetchRunAndSteps: (id: string) => Promise<void>
  appendEvent: (e: AgentEvent) => void
  setEventFilter: (f: EventType | 'all') => void
  clearEvents: () => void
  loadRunHistory: (sessionId: string) => Promise<void>
}

const TERMINAL_EVENTS: ReadonlySet<EventType> = new Set([
  'run.finished',
  'run.failed',
  'run.cancelled',
])

export const useRunStore = create<RunState>((set, get) => ({
  activeRunId: null,
  activeRun: null,
  activeSteps: [],
  events: [],
  eventFilter: 'all',
  runHistory: [],

  activateRun: async (id) => {
    set({ activeRunId: id, events: [] })
    await get().fetchRunAndSteps(id)
  },

  fetchRunAndSteps: async (id) => {
    const { run, steps } = await api.getRun(id)
    set({ activeRun: run, activeSteps: steps })
  },

  appendEvent: (e) => {
    const events = [...get().events, e]
    if (events.length > MAX_EVENTS) {
      events.splice(0, events.length - MAX_EVENTS)
    }
    set({ events })
    if (TERMINAL_EVENTS.has(e.event_type)) {
      const id = get().activeRunId
      if (id) void get().fetchRunAndSteps(id)
    }
  },

  setEventFilter: (f) => set({ eventFilter: f }),

  clearEvents: () => set({ events: [] }),

  loadRunHistory: async (sessionId) => {
    const runs = await api.listRuns(sessionId)
    set({ runHistory: runs })
  },
}))
