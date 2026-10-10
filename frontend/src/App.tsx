import { useEffect } from 'react'
import { Header } from '@/components/Header'
import { SessionPanel } from '@/components/SessionPanel'
import { RunForm } from '@/components/RunForm'
import { RunDetail } from '@/components/RunDetail'
import { EventStream } from '@/components/EventStream'
import { useSessionStore } from '@/store/session-store'
import { useRunStore } from '@/store/run-store'
import { useEventStream } from '@/lib/use-event-stream'

export default function App() {
  const loadSessions = useSessionStore((s) => s.loadSessions)
  const activeSessionId = useSessionStore((s) => s.activeSessionId)

  useEventStream()

  useEffect(() => { void loadSessions() }, [loadSessions])

  useEffect(() => {
    if (activeSessionId) {
      void useRunStore.getState().loadRunHistory(activeSessionId)
    }
  }, [activeSessionId])

  return (
    <div className="relative flex h-screen flex-col bg-background text-foreground">
      <div
        aria-hidden
        className="pointer-events-none absolute inset-x-0 top-0 h-64 bg-[radial-gradient(ellipse_at_top,rgba(59,130,246,0.12),transparent)]"
      />
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
