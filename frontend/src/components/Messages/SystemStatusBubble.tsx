import { useState } from 'react'
import { api, ApiError } from '@/lib/api'
import { cn } from '@/lib/utils'
import { stepStatusClasses, statusBadgeClass, TERMINAL_RUN_STATUSES } from '@/lib/status'
import type { Run, Step } from '@/lib/types'
import { t } from '@/i18n'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'

interface Props {
  run: Run
  steps?: Step[]
  onRetryRun: (runId: string) => void
  onRerunRun?: (run: Run) => void
  progress?: { toolCalls: number; modelCalls: number; seconds: number }
  partialText?: string
}

export function SystemStatusBubble({ run, steps = [], onRetryRun, onRerunRun, progress, partialText }: Props) {
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)
  const isActive = !TERMINAL_RUN_STATUSES.includes(run.status)
  const isFailed = run.status === 'failed' || run.status === 'cancelled' || run.status === 'timed_out'

  const handleCancel = async () => {
    setActionError(null)
    setConfirmOpen(false)
    try {
      await api.cancelRun(run.id)
      onRetryRun(run.id)
    } catch (e) {
      setActionError(e instanceof ApiError ? e.message : t('run.cancelFailed'))
    }
  }

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
                {t('run.cancel')}
              </button>
              <DialogContent className="sm:max-w-[360px]">
                <DialogHeader>
                  <DialogTitle>{t('run.cancelTitle')}</DialogTitle>
                  <DialogDescription>
                    {t('run.cancelDesc')}
                  </DialogDescription>
                </DialogHeader>
                <DialogFooter>
                  <button
                    type="button"
                    className="rounded-md border border-border px-3 py-1.5 text-xs hover:bg-accent"
                    onClick={() => setConfirmOpen(false)}
                  >
                    {t('run.keepRunning')}
                  </button>
                  <button
                    type="button"
                    className="rounded-md bg-rose-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-rose-700"
                    onClick={() => void handleCancel()}
                  >
                    {t('run.cancelRun')}
                  </button>
                </DialogFooter>
              </DialogContent>
            </Dialog>
          )}
          {isFailed ? (
            <button
              type="button"
              className="rounded-md border border-border px-2 py-1 text-xs hover:bg-accent"
              onClick={() => onRerunRun?.(run)}
            >
              {t('run.rerun')}
            </button>
          ) : null}
        </div>
      </div>
      {actionError && <p className="mt-1 text-xs text-rose-600 dark:text-rose-400">{actionError}</p>}
      <p className="mt-2 text-xs text-foreground/90">{run.goal}</p>
      {isActive && partialText && (
        <>
          <p className="mt-1 max-h-40 overflow-hidden text-xs whitespace-pre-wrap text-foreground/80">
            {partialText}
          </p>
          <span className="mt-1 inline-flex items-center gap-1" aria-label={t('run.inputting')}>
            {[0, 150, 300].map((d) => (
              <span
                key={d}
                className="h-1.5 w-1.5 animate-bounce rounded-full bg-muted-foreground"
                style={{ animationDelay: `${d}ms` }}
              />
            ))}
          </span>
        </>
      )}
      {isActive && progress && (
        <p className="mt-1 text-xs text-muted-foreground">
          工具 {progress.toolCalls}/{run.budget.max_tool_calls} · 模型 {progress.modelCalls}/{run.budget.max_model_calls} ·{' '}
          {progress.seconds}s
        </p>
      )}
      {steps.length > 0 && (
        <div className="mt-2">
          <p className="text-xs font-medium text-muted-foreground">
            {t('run.stepsProgress', {
              done: steps.filter((s) => ['succeeded', 'failed', 'skipped'].includes(s.status)).length,
              total: steps.length,
            })}
          </p>
          <ol className="mt-1 space-y-1">
          {steps.map((s) => (
            <li key={s.id} className="flex items-center gap-2 text-xs">
              <span className={cn('h-2 w-2 rounded-full', stepStatusClasses[s.status])} />
              <span className="text-muted-foreground">{s.description}</span>
            </li>
            ))}
          </ol>
        </div>
      )}
    </div>
  )
}
