import { useState } from 'react'
import { RefreshCw, Plus, List, FolderOpen } from 'lucide-react'
import { useSessionStore } from '@/store/session-store'
import { useRunStore } from '@/store/run-store'
import { runStatusClasses, truncate } from '@/lib/status'
import type { RunStatus } from '@/lib/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Separator } from '@/components/ui/separator'
import { cn } from '@/lib/utils'

const runDot: Record<RunStatus, string> = {
  queued: runStatusClasses.queued,
  running: runStatusClasses.running,
  waiting_approval: runStatusClasses.waiting_approval,
  paused: runStatusClasses.paused,
  succeeded: runStatusClasses.succeeded,
  failed: runStatusClasses.failed,
  cancelled: runStatusClasses.cancelled,
  timed_out: runStatusClasses.timed_out,
}

export function SessionPanel() {
  const sessions = useSessionStore((s) => s.sessions)
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const createSession = useSessionStore((s) => s.createSession)
  const switchSession = useSessionStore((s) => s.switchSession)
  const loadSessions = useSessionStore((s) => s.loadSessions)

  const runHistory = useRunStore((s) => s.runHistory)
  const activateRun = useRunStore((s) => s.activateRun)
  const activeRunId = useRunStore((s) => s.activeRunId)

  const [newTitle, setNewTitle] = useState('')
  const [creating, setCreating] = useState(false)

  async function handleCreate() {
    const title = newTitle.trim()
    if (!title || creating) return
    setCreating(true)
    try {
      await createSession(title)
      setNewTitle('')
    } finally {
      setCreating(false)
    }
  }

  return (
    <aside className="flex w-60 shrink-0 flex-col border-r border-zinc-800 bg-zinc-900/50">
      <div className="flex flex-col gap-2 p-3">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-1.5 text-xs font-medium text-zinc-400">
            <FolderOpen className="h-3.5 w-3.5" />
            Sessions
          </div>
          <Button
            variant="ghost"
            size="icon"
            className="h-7 w-7 text-zinc-400 hover:text-zinc-200"
            onClick={() => void loadSessions()}
            title="Refresh sessions"
          >
            <RefreshCw className="h-3.5 w-3.5" />
          </Button>
        </div>

        <div className="flex gap-1.5">
          <Input
            value={newTitle}
            onChange={(e) => setNewTitle(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') void handleCreate()
            }}
            placeholder="New session title…"
            className="h-8 text-xs"
          />
          <Button
            size="sm"
            className="h-8 px-2"
            onClick={() => void handleCreate()}
            disabled={!newTitle.trim() || creating}
          >
            <Plus className="h-3.5 w-3.5" />
          </Button>
        </div>

        <div className="flex flex-col gap-0.5">
          {sessions.length === 0 && (
            <div className="px-2 py-1 text-xs text-zinc-500">No sessions</div>
          )}
          {sessions.map((s) => {
            const active = s.session_id === activeSessionId
            return (
              <button
                key={s.session_id}
                onClick={() => switchSession(s.session_id)}
                className={cn(
                  'truncate rounded px-2 py-1.5 text-left text-xs',
                  active
                    ? 'bg-zinc-800 text-zinc-100'
                    : 'text-zinc-400 hover:bg-zinc-800/50 hover:text-zinc-200',
                )}
              >
                {s.title || s.session_id}
              </button>
            )
          })}
        </div>
      </div>

      <Separator />

      <div className="flex flex-1 flex-col gap-2 overflow-y-auto p-3">
        <div className="flex items-center gap-1.5 text-xs font-medium text-zinc-400">
          <List className="h-3.5 w-3.5" />
          Run History
        </div>
        <div className="flex flex-col gap-1">
          {runHistory.length === 0 && (
            <div className="px-2 py-1 text-xs text-zinc-500">No runs</div>
          )}
          {runHistory.map((run) => {
            const active = run.id === activeRunId
            return (
              <button
                key={run.id}
                onClick={() => void activateRun(run.id)}
                className={cn(
                  'flex flex-col gap-1 rounded border p-2 text-left',
                  active
                    ? 'border-zinc-700 bg-zinc-800/70'
                    : 'border-transparent hover:border-zinc-800 hover:bg-zinc-800/40',
                )}
              >
                <span className="truncate text-xs text-zinc-200">
                  {truncate(run.goal || run.id, 40)}
                </span>
                <span
                  className={cn(
                    'inline-flex w-fit border rounded-full px-1.5 py-0.5 text-[10px]',
                    runDot[run.status],
                  )}
                >
                  {run.status}
                </span>
              </button>
            )
          })}
        </div>
      </div>
    </aside>
  )
}
