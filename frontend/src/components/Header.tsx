import { useUiStore } from '@/store/ui-store'
import { cn } from '@/lib/utils'
import { SettingsDialog } from '@/components/SettingsDialog'

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
      <h1 className="bg-gradient-to-r from-blue-200 via-blue-400 to-cyan-300 bg-clip-text text-sm font-semibold tracking-tight text-transparent">
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
        <SettingsDialog />
      </div>
    </header>
  )
}
