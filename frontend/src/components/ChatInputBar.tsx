import { useState } from 'react'
import { useSessionStore } from '@/store/session-store'
import { useRunStore } from '@/store/run-store'
import { isActiveRunStatus } from '@/lib/status'

import { NewRunDialog } from '@/components/NewRunDialog'

export function ChatInputBar() {
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const submitRun = useRunStore((s) => s.submitRun)
  const activeRun = useRunStore((s) => s.activeRun)
  const cancelling = useRunStore((s) => s.cancelling)
  const cancelError = useRunStore((s) => s.cancelError)
  const cancelActiveRun = useRunStore((s) => s.cancelActiveRun)
  const pendingGoals = useRunStore((s) => s.pendingGoals)
  const [goal, setGoal] = useState('')
  const [dialogOpen, setDialogOpen] = useState(false)

  const busy = pendingGoals.some((p) => p.status === 'pending')

  const stopping = activeRun !== null && isActiveRunStatus(activeRun.status)

  const submit = () => {
    const trimmed = goal.trim()
    if (!trimmed || busy || !activeSessionId) return
    setGoal('')
    void submitRun({
      sessionId: activeSessionId,
      goal: trimmed,
      mode: undefined,
      constraints: undefined,
      successCriteria: undefined,
      budget: undefined,
    })
  }

  return (
    <div className="border-t border-border bg-background/80 p-3 backdrop-blur">
      <div className="flex items-end gap-2">
        <textarea
          className="max-h-40 min-h-[3rem] flex-1 resize-y rounded-lg border border-border bg-card p-3 text-sm focus:outline-none focus:ring-2 focus:ring-ring/40"
          placeholder="Describe the task for this session…"
          rows={1}
          value={goal}
          onChange={(e) => setGoal(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && (e.ctrlKey || e.metaKey) && !e.shiftKey) {
              e.preventDefault()
              submit()
            }
          }}
        />
        <button
          type="button"
          title="Advanced settings"
          aria-label="Advanced settings"
          className="rounded-lg border border-border p-2.5 text-muted-foreground hover:bg-accent hover:text-foreground"
          onClick={() => setDialogOpen(true)}
        >
          ⚙
        </button>
        {stopping ? (
          <button
            type="button"
            className="rounded-lg bg-rose-600 px-4 py-2.5 text-sm font-medium text-rose-50 hover:bg-rose-500 disabled:opacity-50"
            disabled={busy || cancelling}
            onClick={() => activeRun && void cancelActiveRun(activeRun.id)}
          >
            {cancelling ? 'Stopping…' : '⏹ Stop'}
          </button>
        ) : (
          <button
            type="button"
            className="rounded-lg bg-primary px-4 py-2.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
            disabled={busy || !goal.trim()}
            onClick={submit}
          >
            Send
          </button>
        )}
      </div>
      {cancelError && (
        <p className="mt-2 text-xs text-rose-600 dark:text-rose-400">{cancelError}</p>
      )}
      <NewRunDialog open={dialogOpen} onOpenChange={setDialogOpen} />
    </div>
  )
}
