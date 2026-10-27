import { useState } from 'react'
import { History, Sun, Moon, Monitor } from 'lucide-react'
import { useUiStore } from '@/store/ui-store'
import { cn } from '@/lib/utils'
import { SettingsDialog } from '@/components/SettingsDialog'
import { RunsHistoryDialog } from '@/components/RunsHistoryDialog'

function ThemeToggle() {
  const theme = useUiStore((s) => s.theme)
  const label = theme === 'light' ? '浅色' : theme === 'dark' ? '深色' : '跟随系统'
  const ThemeIcon = theme === 'light' ? Sun : theme === 'dark' ? Moon : Monitor
  return (
    <button
      type="button"
      aria-label="切换主题"
      title={`主题：${label}`}
      onClick={() => useUiStore.getState().cycleTheme()}
      className="flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
    >
      <ThemeIcon className="h-4 w-4" />
    </button>
  )
}

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
  const [historyOpen, setHistoryOpen] = useState(false)

  const streamColor =
    sseStatus === 'polling'
      ? 'bg-emerald-400'
      : sseStatus === 'error'
        ? 'bg-amber-400'
        : 'bg-muted'

  return (
    <>
      <header className="flex h-12 shrink-0 items-center justify-between border-b border-border bg-background px-4">
        <h1 className="bg-gradient-to-r from-blue-200 via-blue-400 to-cyan-300 bg-clip-text text-sm font-semibold tracking-tight text-transparent">
          Rivus Agent
        </h1>
        <div className="flex items-center gap-2">
          <ThemeToggle />
          <button
            type="button"
            aria-label="运行历史"
            title="运行历史"
            onClick={() => setHistoryOpen(true)}
            className="flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
          >
            <History className="h-4 w-4" />
          </button>
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
      <RunsHistoryDialog open={historyOpen} onOpenChange={setHistoryOpen} />
      {sseStatus === 'error' && (
        <div className="flex h-8 shrink-0 items-center justify-center gap-2 border-b border-amber-400/40 bg-amber-400/10 px-4 text-xs text-amber-600 dark:text-amber-400">
          <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-amber-400" />
          Stream connection lost — reconnecting, events may be delayed
        </div>
      )}
    </>
  )
}
