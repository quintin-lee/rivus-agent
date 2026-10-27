import { useCallback, useEffect, useState } from 'react'
import { History, RefreshCw } from 'lucide-react'
import { api } from '@/lib/api'
import { runStatusClasses } from '@/lib/status'
import type { Run, RunStatus } from '@/lib/types'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

function relTime(ts: number): string {
  if (!ts) return ''
  const diff = Date.now() - ts
  const m = Math.floor(diff / 60000)
  if (m < 1) return '刚刚'
  if (m < 60) return `${m} 分钟前`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h} 小时前`
  return `${Math.floor(h / 24)} 天前`
}

const statusLabel: Record<string, string> = {
  queued: '排队中',
  running: '运行中',
  waiting_approval: '待审批',
  paused: '已暂停',
  succeeded: '成功',
  failed: '失败',
  cancelled: '已取消',
  timed_out: '超时',
}

export function RunsHistoryDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (v: boolean) => void
}) {
  const [runs, setRuns] = useState<Run[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      setRuns(await api.listRuns(undefined, 50))
    } catch (e) {
      setError(e instanceof Error ? e.message : '加载失败')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    if (open) void load()
  }, [open, load])

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[70vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-1.5">
            <History className="h-4 w-4" />
            运行历史
          </DialogTitle>
          <DialogDescription>跨会话的全部顶层运行（最新在前，不含子 agent 运行）。</DialogDescription>
        </DialogHeader>
        <div className="flex justify-end">
          <Button
            variant="ghost"
            size="icon"
            className="h-7 w-7"
            onClick={() => void load()}
            title="刷新"
          >
            <RefreshCw className="h-3.5 w-3.5" />
          </Button>
        </div>
        {loading && <div className="text-xs text-muted-foreground">加载中…</div>}
        {error && (
          <div className="flex items-center gap-2 text-xs text-red-400">
            <span>{error}</span>
            <Button variant="ghost" size="sm" className="h-6 px-2" onClick={() => void load()}>
              重试
            </Button>
          </div>
        )}
        {!loading && !error && runs.length === 0 && (
          <div className="text-xs text-muted-foreground/70">（无运行记录）</div>
        )}
        <div className="flex flex-col gap-2">
          {runs.map((r) => (
            <div key={r.id} className="rounded border border-border bg-muted/30 p-2">
              <div className="flex items-center gap-2">
                <span
                  className={`rounded-full px-1.5 py-0.5 text-[10px] font-medium ${runStatusClasses[r.status as RunStatus] ?? ''}`}
                >
                  {statusLabel[r.status] ?? r.status}
                </span>
                <span className="truncate text-xs text-foreground" title={r.goal}>
                  {r.goal}
                </span>
              </div>
              <div className="mt-1 flex items-center gap-2 text-[10px] text-muted-foreground/70">
                <span title={r.session_id}>会话 {r.session_id?.slice(0, 8)}</span>
                <span>{relTime(r.created_at)}</span>
                {r.error_code && <span className="text-red-400">{r.error_code}</span>}
              </div>
            </div>
          ))}
        </div>
      </DialogContent>
    </Dialog>
  )
}
