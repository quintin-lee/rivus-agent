import { useUiStore } from '@/store/ui-store'
import { cn } from '@/lib/utils'

function StatusDot({ color, label, title }: { color: string; label: string; title: string }) {
  return (
    <div
      title={title}
      className="flex items-center gap-1.5 rounded-full border border-border bg-card px-2.5 py-1 text-xs text-muted-foreground"
    >
      <span className={cn('h-2 w-2 rounded-full', color)} />
      <span>{label}</span>
    </div>
  )
}

export function Header() {
  const sseStatus = useUiStore((s) => s.sseStatus)
  const apiReachable = useUiStore((s) => s.apiReachable)

  const streamColor =
    sseStatus === 'polling'
      ? 'bg-emerald-400'
      : sseStatus === 'error'
        ? 'bg-amber-400'
        : 'bg-muted'

  return (
    <header className="flex h-12 shrink-0 items-center justify-between border-b border-border bg-background px-4">
      <h1 className="text-sm font-semibold tracking-tight text-foreground">
        Rivus Agent
      </h1>
      <div className="flex items-center gap-2">
        <StatusDot
          color={apiReachable ? 'bg-emerald-400' : 'bg-red-500'}
          label="API"
          title={apiReachable ? 'API reachable' : 'API unreachable'}
        />
        <StatusDot
          color={streamColor}
          label="Stream"
          title={`Stream: ${sseStatus}`}
        />
      </div>
    </header>
  )
}
