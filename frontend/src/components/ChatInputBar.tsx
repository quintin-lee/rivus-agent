import { useState } from 'react'
import { useSessionStore } from '@/store/session-store'
import { useRunStore } from '@/store/run-store'
import { NewRunDialog } from '@/components/NewRunDialog'

export function ChatInputBar() {
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const submitRun = useRunStore((s) => s.submitRun)
  const pendingGoals = useRunStore((s) => s.pendingGoals)
  const [goal, setGoal] = useState('')
  const [dialogOpen, setDialogOpen] = useState(false)

  const busy = pendingGoals.some((p) => p.status === 'pending')

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
        <button
          type="button"
          className="rounded-lg bg-primary px-4 py-2.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
          disabled={busy || !goal.trim()}
          onClick={submit}
        >
          Send
        </button>
      </div>
      <NewRunDialog open={dialogOpen} onOpenChange={setDialogOpen} />
    </div>
  )
}
