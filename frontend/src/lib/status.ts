import type { RunStatus, StepStatus, EventType } from '@/lib/types'
import { cn } from '@/lib/utils'

export const runStatusClasses: Record<RunStatus, string> = {
  queued: 'bg-muted text-foreground/90 border-border',
  running: 'bg-blue-500/15 text-blue-400 border-blue-500/30',
  waiting_approval: 'bg-amber-500/15 text-amber-400 border-amber-500/30',
  paused: 'bg-violet-500/15 text-violet-400 border-violet-500/30',
  succeeded: 'bg-emerald-500/15 text-emerald-400 border-emerald-500/30',
  failed: 'bg-red-500/15 text-red-400 border-red-500/30',
  cancelled: 'bg-muted text-foreground/90 border-border',
  timed_out: 'bg-orange-500/15 text-orange-400 border-orange-500/30',
}

export const runStatusDot: Record<RunStatus, string> = {
  queued: 'bg-muted-foreground',
  running: 'bg-blue-400',
  waiting_approval: 'bg-amber-400',
  paused: 'bg-violet-400',
  succeeded: 'bg-emerald-400',
  failed: 'bg-red-400',
  cancelled: 'bg-muted-foreground/70',
  timed_out: 'bg-orange-400',
}

export const stepStatusClasses: Record<StepStatus, string> = {
  pending: 'bg-muted text-muted-foreground border-border',
  running: 'bg-blue-500/15 text-blue-400 border-blue-500/30',
  succeeded: 'bg-emerald-500/15 text-emerald-400 border-emerald-500/30',
  failed: 'bg-red-500/15 text-red-400 border-red-500/30',
  skipped: 'bg-muted text-muted-foreground border-border',
  awaiting_approval: 'bg-amber-500/15 text-amber-400 border-amber-500/30',
}

export const TERMINAL_RUN_STATUSES: readonly RunStatus[] = [
  'succeeded',
  'failed',
  'cancelled',
  'timed_out',
]

export const isActiveRunStatus = (s: RunStatus): boolean =>
  !TERMINAL_RUN_STATUSES.includes(s)

type EventCategory = 'run' | 'model' | 'tool' | 'step' | 'approval' | 'other'

export function eventCategory(type: EventType): EventCategory {
  switch (type) {
    case 'run.created':
    case 'run.started':
    case 'run.finished':
    case 'run.failed':
    case 'run.cancelled':
    case 'run.resumed':
      return 'run'
    case 'model.requested':
    case 'model.completed':
    case 'model.failed':
      return 'model'
    case 'tool.requested':
    case 'tool.authorized':
    case 'tool.denied':
    case 'tool.started':
    case 'tool.completed':
    case 'tool.failed':
      return 'tool'
    case 'step.started':
    case 'step.finished':
      return 'step'
    case 'approval.requested':
    case 'approval.decided':
      return 'approval'
    default:
      return 'other'
  }
}

export const eventCategoryClasses: Record<EventCategory, string> = {
  run: 'bg-muted text-foreground/90 border-border',
  model: 'bg-sky-500/15 text-sky-400 border-sky-500/30',
  tool: 'bg-teal-500/15 text-teal-400 border-teal-500/30',
  step: 'bg-violet-500/15 text-violet-400 border-violet-500/30',
  approval: 'bg-amber-500/15 text-amber-400 border-amber-500/30',
  other: 'bg-muted text-muted-foreground border-border',
}

export function statusBadgeClass(status: RunStatus | StepStatus, step = false) {
  if (step) {
    return cn(
      'border px-2 py-0.5 text-xs rounded-full font-medium',
      stepStatusClasses[status as StepStatus],
    )
  }
  return cn(
    'border px-2 py-0.5 text-xs rounded-full font-medium',
    runStatusClasses[status as RunStatus],
  )
}

export function formatTimestamp(ts: number): string {
  if (!ts) return '--:--:--'
  const d = new Date(ts)
  return d.toLocaleTimeString('en-GB', {
    hour12: false,
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
}

export function truncate(s: string, n: number): string {
  if (!s) return ''
  if (s.length <= n) return s
  return s.slice(0, n - 1) + '…'
}
