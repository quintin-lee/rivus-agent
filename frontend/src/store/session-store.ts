import { create } from 'zustand'
import { api } from '@/lib/api'
import type { Session } from '@/lib/types'
import { useRunStore } from '@/store/run-store'

interface SessionState {
  sessions: Session[]
  activeSessionId: string | null
  createSession: (title: string) => Promise<void>
  switchSession: (id: string) => void
  loadSessions: () => Promise<void>
  deleteSession: (id: string) => Promise<void>
}

export const useSessionStore = create<SessionState>((set, get) => ({
  sessions: [],
  activeSessionId: null,

  createSession: async (title) => {
    const res = await api.createSession(title)
    const newSession: Session = {
      session_id: res.session_id,
      title,
      created_at: Date.now(),
      updated_at: Date.now(),
    }
    set({
      sessions: [...get().sessions, newSession],
      activeSessionId: res.session_id,
    })
  },

  switchSession: (id) => set({ activeSessionId: id }),

  loadSessions: async () => {
    const sessions = await api.listSessions()
    set((state) => ({
      sessions,
      activeSessionId: state.activeSessionId ?? sessions[0]?.session_id ?? null,
    }))
  },

  deleteSession: async (id) => {
    await api.deleteSession(id)
    const remaining = get().sessions.filter((s) => s.session_id !== id)
    const wasActive = get().activeSessionId === id
    set({
      sessions: remaining,
      activeSessionId: wasActive
        ? (remaining[0]?.session_id ?? null)
        : get().activeSessionId,
    })
    if (wasActive) {
      useRunStore.setState({
        activeRunId: null,
        activeRun: null,
        activeSteps: [],
        events: [],
        runHistory: [],
      })
    }
  },
}))
