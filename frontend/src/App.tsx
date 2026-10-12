import { useEffect } from 'react'
import { Header } from '@/components/Header'
import { SessionList } from '@/components/SessionList'
import { ChatMessageList } from '@/components/ChatMessageList'
import { ChatInputBar } from '@/components/ChatInputBar'
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
        <SessionList />
        <main className="flex flex-1 flex-col overflow-hidden">
          <div className="flex-1 overflow-y-auto p-4">
            <ChatMessageList />
          </div>
          <ChatInputBar />
        </main>
      </div>
    </div>
  )
}
