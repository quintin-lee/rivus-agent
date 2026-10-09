import { useEffect } from 'react'
import { Header } from '@/components/Header'
import { SessionPanel } from '@/components/SessionPanel'
import { RunForm } from '@/components/RunForm'
import { RunDetail } from '@/components/RunDetail'
import { EventStream } from '@/components/EventStream'
import { useSessionStore } from '@/store/session-store'
import { useRunStore } from '@/store/run-store'

export default function App() {
  const loadSessions = useSessionStore((s) => s.loadSessions)
  const activeSessionId = useSessionStore((s) => s.activeSessionId)

  useEffect(() => { void loadSessions() }, [loadSessions])

  useEffect(() => {
    if (activeSessionId) {
      void useRunStore.getState().loadRunHistory(activeSessionId)
    }
  }, [activeSessionId])

  return (
    <div className="flex h-screen flex-col bg-zinc-950 text-zinc-100">
      <Header />
      <div className="flex flex-1 overflow-hidden">
        <SessionPanel />
        <main className="flex flex-1 flex-col gap-4 overflow-y-auto p-4">
          <RunForm />
          <RunDetail />
          <EventStream />
        </main>
      </div>
    </div>
  )
}
