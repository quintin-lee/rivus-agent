import { useState } from 'react'
import {
  Square,
  Play,
  ShieldCheck,
  CircleCheck,
  CircleX,
  Hourglass,
  Clock,
} from 'lucide-react'
import { useRunStore } from '@/store/run-store'
import { api, ApiError } from '@/lib/api'
import { isActiveRunStatus } from '@/lib/status'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
  DialogDescription,
} from '@/components/ui/dialog'

function scrollToEventStream() {
  const el = document.getElementById('event-stream')
  if (el) el.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

export function ActionBar() {
  const activeRun = useRunStore((s) => s.activeRun)
  const fetchRunAndSteps = useRunStore((s) => s.fetchRunAndSteps)

  const [cancelOpen, setCancelOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  if (!activeRun) return null

  const runId = activeRun.id
  const terminal = !isActiveRunStatus(activeRun.status)

  const statusText: Record<string, { icon: React.ReactNode; text: string }> = {
    succeeded: { icon: <CircleCheck className="h-4 w-4 text-emerald-400" />, text: 'Run succeeded' },
    failed: { icon: <CircleX className="h-4 w-4 text-red-400" />, text: 'Run failed' },
    cancelled: { icon: <CircleX className="h-4 w-4 text-muted-foreground" />, text: 'Run cancelled' },
    timed_out: { icon: <Clock className="h-4 w-4 text-orange-400" />, text: 'Run timed out' },
  }

  async function confirmCancel() {
    setBusy(true)
    try {
      await api.cancelRun(runId)
      setCancelOpen(false)
      await fetchRunAndSteps(runId)
    } finally {
      setBusy(false)
    }
  }

  async function handleResume() {
    setBusy(true)
    setError('')
    try {
      await api.resumeRun(runId)
      await fetchRunAndSteps(runId)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : '当前状态不可重试')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex flex-wrap items-center gap-2">
      {activeRun.status === 'waiting_approval' && (
        <Button
          size="sm"
          variant="outline"
          onClick={scrollToEventStream}
          className="border-amber-500/40 text-amber-400 hover:bg-amber-500/10 hover:text-amber-300"
        >
          <ShieldCheck className="h-3.5 w-3.5" />
          Review Approval
        </Button>
      )}

      {activeRun.status === 'paused' && (
        <Button
          size="sm"
          variant="outline"
          onClick={() => void handleResume()}
          disabled={busy}
          className="border-violet-500/40 text-violet-400 hover:bg-violet-500/10 hover:text-violet-300"
        >
          <Play className="h-3.5 w-3.5" />
          Resume
        </Button>
      )}

      {activeRun.status === 'failed' && (
        <Button
          size="sm"
          variant="outline"
          onClick={() => void handleResume()}
          disabled={busy}
          className="border-violet-500/40 text-violet-400 hover:bg-violet-500/10 hover:text-violet-300"
        >
          <Play className="h-3.5 w-3.5" />
          重试
        </Button>
      )}

      {error && <span className="text-xs text-red-400">{error}</span>}

      {isActiveRunStatus(activeRun.status) && (
        <Dialog open={cancelOpen} onOpenChange={setCancelOpen}>
          <Button
            size="sm"
            variant="outline"
            className="border-red-500/40 text-red-400 hover:bg-red-500/10 hover:text-red-300"
            onClick={() => setCancelOpen(true)}
            disabled={busy}
          >
            <Square className="h-3.5 w-3.5" />
            Cancel
          </Button>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Cancel run?</DialogTitle>
              <DialogDescription>
                This will stop the run. Completed steps are preserved; in-flight
                work will be interrupted.
              </DialogDescription>
            </DialogHeader>
            <DialogFooter>
              <Button variant="ghost" onClick={() => setCancelOpen(false)}>
                Keep running
              </Button>
              <Button variant="destructive" onClick={() => void confirmCancel()} disabled={busy}>
                {busy ? 'Cancelling…' : 'Cancel run'}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      )}

      {terminal && (
        <div className="flex items-center gap-2 rounded-md border border-border bg-card px-3 py-1.5 text-sm text-foreground/90">
          {statusText[activeRun.status]?.icon ?? <Hourglass className="h-4 w-4" />}
          <span>{statusText[activeRun.status]?.text ?? activeRun.status}</span>
        </div>
      )}
    </div>
  )
}
