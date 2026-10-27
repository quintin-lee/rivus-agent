import { useState } from 'react'
import { Bot, ChevronDown, ChevronRight, RefreshCw } from 'lucide-react'
import { api } from '@/lib/api'
import { runStatusClasses, truncate } from '@/lib/status'
import type { Run } from '@/lib/types'
import { cn } from '@/lib/utils'

type SubagentMessage = {
  kind: 'system-subagent'
  id: string
  runId: string
  run: Run
  count: number
}

function durationSec(r: Run): number {
  const s = r.started_at ?? r.created_at
  const e = r.finished_at ?? r.updated_at
  if (!s || !e || e <= s) return 0
  return Math.floor((e - s) / 1000)
}

export function SubagentCard({ message }: { message: SubagentMessage }) {
  const [open, setOpen] = useState(false)
  const [children, setChildren] = useState<Run[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  async function load() {
    setLoading(true)
    setError('')
    try {
      const all = await api.listRuns(message.run.session_id, 200)
      setChildren(
        all
          .filter((r) => r.parent_run_id === message.runId)
          .sort((a, b) => a.created_at - b.created_at),
      )
    } catch (e) {
      setError(e instanceof Error ? e.message : '加载失败')
    } finally {
      setLoading(false)
    }
  }

  function toggle() {
    const next = !open
    setOpen(next)
    if (next && children.length === 0 && !error) void load()
  }

  return (
    <div className="rounded-lg border border-border bg-card/60 px-3 py-2">
      <div className="flex items-center gap-2">
        <button
          onClick={toggle}
          className="flex min-w-0 flex-1 items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground"
        >
          {open ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
          <Bot className="h-3.5 w-3.5" />
          <span>子 agent ×{message.count}</span>
        </button>
        {open && (
          <button
            onClick={() => void load()}
            title="刷新"
            className="text-muted-foreground/70 hover:text-foreground"
          >
            <RefreshCw className={cn('h-3 w-3', loading && 'animate-spin')} />
          </button>
        )}
      </div>
      {open && (
        <div className="mt-2 flex flex-col gap-1.5 border-t border-border pt-2">
          {loading && <div className="text-xs text-muted-foreground">加载中…</div>}
          {error && (
            <div className="flex items-center gap-2 text-xs text-red-400">
              <span>{error}</span>
              <button className="underline" onClick={() => void load()}>
                重试
              </button>
            </div>
          )}
          {!loading && !error && children.length === 0 && (
            <div className="text-xs text-muted-foreground/70">（无子 run 记录）</div>
          )}
          {children.map((r) => (
            <div key={r.id} className="flex items-center gap-2 text-xs">
              <span
                className={cn(
                  'inline-flex w-fit shrink-0 rounded-full border px-1.5 py-0.5 text-[10px]',
                  runStatusClasses[r.status],
                )}
              >
                {r.status}
              </span>
              <span className="min-w-0 flex-1 truncate text-foreground">
                {truncate(r.goal || r.id, 60)}
              </span>
              {r.error_code && <span className="shrink-0 text-red-400">{r.error_code}</span>}
              <span className="shrink-0 text-muted-foreground/70">{durationSec(r)}s</span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
