import { useState } from 'react'
import { api } from '@/lib/api'
import { cn } from '@/lib/utils'
import { stepStatusClasses, statusBadgeClass, TERMINAL_RUN_STATUSES } from '@/lib/status'
import type { Run, Step } from '@/lib/types'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'

interface Props {
  run: Run
  steps?: Step[]
  onCancelRun: (runId: string) => void
  onRetryRun: (runId: string) => void
}

export function SystemStatusBubble({ run, steps = [], onCancelRun, onRetryRun }: Props) {
  const [confirmOpen, setConfirmOpen] = useState(false)
  const isActive = !TERMINAL_RUN_STATUSES.includes(run.status)
  const isFailed = run.status === 'failed' || run.status === 'cancelled' || run.status === 'timed_out'

  return (
    <div className="rounded-lg border border-border bg-card/50 p-3 text-sm">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <span className={cn('rounded-full px-2 py-0.5 text-[11px]', statusBadgeClass(run.status))}>
            {run.status.replace('_', ' ')}
          </span>
          <span className="text-xs text-muted-foreground">{run.mode}</span>
        </div>
        <div className="flex items-center gap-2">
          {isActive && (
            <Dialog open={confirmOpen} onOpenChange={setConfirmOpen}>
              <button
                type="button"
                className="rounded-md border border-border px-2 py-1 text-xs hover:bg-accent"
                onClick={() => setConfirmOpen(true)}
              >
                Cancel
              </button>
              <DialogContent className="sm:max-w-[360px]">
                <DialogHeader>
                  <DialogTitle>Cancel this run?</DialogTitle>
                  <DialogDescription>
                    The run will stop and cannot be resumed from this UI.
                  </DialogDescription>
                </DialogHeader>
                <DialogFooter>
                  <button
                    type="button"
                    className="rounded-md border border-border px-3 py-1.5 text-xs hover:bg-accent"
                    onClick={() => setConfirmOpen(false)}
                  >
                    Keep running
                  </button>
                  <button
                    type="button"
                    className="rounded-md bg-rose-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-rose-700"
                    onClick={async () => {
                      setConfirmOpen(false)
                      onCancelRun(run.id)
                      try {
                        await api.cancelRun(run.id)
                      } catch {
                        // error surfaced by global api error handling
                      }
                    }}
                  >
                    Cancel run
                  </button>
                </DialogFooter>
              </DialogContent>
            </Dialog>
          )}
          {isFailed ? (
            <button
              type="button"
              className="rounded-md border border-border px-2 py-1 text-xs hover:bg-accent"
              onClick={() => {
                onRetryRun(run.id)
                void api.resumeRun(run.id).catch(() => {})
              }}
            >
              Retry
            </button>
          ) : null}
        </div>
      </div>
      <p className="mt-2 text-xs text-foreground/90">{run.goal}</p>
      {steps.length > 0 && (
        <ol className="mt-2 space-y-1">
          {steps.map((s) => (
            <li key={s.id} className="flex items-center gap-2 text-xs">
              <span className={cn('h-2 w-2 rounded-full', stepStatusClasses[s.status])} />
              <span className="text-muted-foreground">{s.description}</span>
            </li>
          ))}
        </ol>
      )}
    </div>
  )
}
