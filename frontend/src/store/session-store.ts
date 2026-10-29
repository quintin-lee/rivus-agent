import { create } from 'zustand'
import { api } from '@/lib/api'
import type { Session } from '@/lib/types'
import { useRunStore } from '@/store/run-store'

interface SessionState {
  sessions: Session[]
  activeSessionId: string | null
  sessionsCursor: string
  createSession: (title: string) => Promise<void>
  switchSession: (id: string) => void
  loadSessions: (q?: string) => Promise<void>
  loadMoreSessions: (q?: string) => Promise<void>
  deleteSession: (id: string) => Promise<void>
  renameSession: (id: string, title: string) => Promise<void>
}

export const useSessionStore = create<SessionState>((set, get) => ({
  sessions: [],
  activeSessionId: null,
  sessionsCursor: '',

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

  switchSession: (id) => {
    set({ activeSessionId: id })
    useRunStore.setState({
      activeRunId: null,
      activeRun: null,
      activeSteps: [],
      events: [],
    })
  },

  loadSessions: async (q?: string) => {
    const page = await api.listSessionsPage(q || undefined, undefined)
    set((state) => ({
      sessions: page.items,
      sessionsCursor: page.nextCursor,
      activeSessionId: state.activeSessionId ?? page.items[0]?.session_id ?? null,
    }))
  },

  loadMoreSessions: async (q?: string) => {
    if (!get().sessionsCursor) return
    const page = await api.listSessionsPage(q || undefined, get().sessionsCursor)
    set((state) => ({
      sessions: [...state.sessions, ...page.items],
      sessionsCursor: page.nextCursor,
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

  renameSession: async (id, title) => {
    const s = await api.renameSession(id, title)
    set((state) => ({
      sessions: state.sessions.map((x) =>
        x.session_id === id ? { ...x, title: s.title, updated_at: s.updated_at } : x,
      ),
    }))
  },
}))
