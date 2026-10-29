import { useCallback, useEffect, useState } from 'react'
import { History, RefreshCw, Search } from 'lucide-react'
import { api } from '@/lib/api'
import { t } from '@/i18n'
import { useUiStore } from '@/store/ui-store'
import { runStatusClasses } from '@/lib/status'
import type { Run, RunStatus } from '@/lib/types'
import { RunDetailDialog } from '@/components/RunDetailDialog'
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
  if (m < 1) return t('time.justNow')
  if (m < 60) return t('time.minAgo', { n: m })
  const h = Math.floor(m / 60)
  if (h < 24) return t('time.hourAgo', { n: h })
  return t('time.dayAgo', { n: Math.floor(h / 24) })
}

const KNOWN_STATUSES = new Set([
  'queued',
  'running',
  'waiting_approval',
  'paused',
  'succeeded',
  'failed',
  'cancelled',
  'timed_out',
])

export function RunsHistoryDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (v: boolean) => void
}) {
  // 订阅 lang：切换语言时触发重渲染（t() 内部读取 store 最新值）
  useUiStore((s) => s.lang)
  const [runs, setRuns] = useState<Run[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [detailRunId, setDetailRunId] = useState<string | null>(null)
  const [statusFilter, setStatusFilter] = useState('')
  const [searchDraft, setSearchDraft] = useState('')
  const [query, setQuery] = useState('')
  const [runsCursor, setRunsCursor] = useState('')

  const statusChips: [string, string][] = [
    ['', t('runs.filterAll')],
    ['succeeded', t('status.succeeded')],
    ['failed', t('status.failed')],
    ['cancelled', t('status.cancelled')],
    ['timed_out', t('status.timed_out')],
  ]

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const page = await api.listRunsPage(
        undefined,
        50,
        statusFilter || undefined,
        query || undefined,
        undefined,
      )
      setRuns(page.items)
      setRunsCursor(page.nextCursor)
    } catch (e) {
      setError(e instanceof Error ? e.message : t('common.loadFailed'))
    } finally {
      setLoading(false)
    }
  }, [statusFilter, query])

  const loadMore = useCallback(async () => {
    if (!runsCursor) return
    setLoading(true)
    setError('')
    try {
      const page = await api.listRunsPage(
        undefined,
        50,
        statusFilter || undefined,
        query || undefined,
        runsCursor,
      )
      setRuns((prev) => [...prev, ...page.items])
      setRunsCursor(page.nextCursor)
    } catch (e) {
      setError(e instanceof Error ? e.message : t('common.loadFailed'))
    } finally {
      setLoading(false)
    }
  }, [statusFilter, query, runsCursor])

  useEffect(() => {
    if (open) void load()
  }, [open, load])

  // 搜索防抖：输入停止 200ms 后更新 query（load 依赖 query 自动重拉第 1 页）
  useEffect(() => {
    const id = setTimeout(() => setQuery(searchDraft), 200)
    return () => clearTimeout(id)
  }, [searchDraft])

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[70vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-1.5">
            <History className="h-4 w-4" />
            {t('runs.title')}
          </DialogTitle>
          <DialogDescription>{t('runs.description')}</DialogDescription>
        </DialogHeader>
        <div className="relative">
          <Search className="absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
          <input
            value={searchDraft}
            onChange={(e) => setSearchDraft(e.target.value)}
            placeholder={t('runs.searchPlaceholder')}
            className="h-8 w-full rounded-md border border-border bg-background pl-7 pr-2 text-xs outline-none focus:ring-1 focus:ring-ring"
          />
        </div>

        <div className="flex flex-wrap gap-1.5">
          {statusChips.map(([value, label]) => (
            <button
              key={value || 'all'}
              onClick={() => setStatusFilter(value)}
              className={
                'rounded-full border px-2.5 py-0.5 text-xs transition-colors ' +
                (statusFilter === value
                  ? 'border-primary bg-primary/10 text-primary'
                  : 'border-border text-muted-foreground hover:text-foreground')
              }
            >
              {label}
            </button>
          ))}
        </div>
        <div className="flex justify-end">
          <Button
            variant="ghost"
            size="icon"
            className="h-7 w-7"
            onClick={() => void load()}
            title={t('common.refresh')}
          >
            <RefreshCw className="h-3.5 w-3.5" />
          </Button>
        </div>
        {loading && <div className="text-xs text-muted-foreground">{t('common.loading')}</div>}
        {error && (
          <div className="flex items-center gap-2 text-xs text-red-400">
            <span>{error}</span>
            <Button variant="ghost" size="sm" className="h-6 px-2" onClick={() => void load()}>
              {t('common.retry')}
            </Button>
          </div>
        )}
        {!loading && !error && runs.length === 0 && (
          <div className="text-xs text-muted-foreground/70">{t('runs.empty')}</div>
        )}
        <div className="flex flex-col gap-2">
          {runs.map((r) => (
            <div
              key={r.id}
              className="cursor-pointer rounded border border-border bg-muted/30 p-2 transition-colors hover:bg-muted/60"
              onClick={() => setDetailRunId(r.id)}
            >
              <div className="flex items-center gap-2">
                <span
                  className={`rounded-full px-1.5 py-0.5 text-[10px] font-medium ${runStatusClasses[r.status as RunStatus] ?? ''}`}
                >
                  {KNOWN_STATUSES.has(r.status) ? t(`status.${r.status}`) : r.status}
                </span>
                <span className="truncate text-xs text-foreground" title={r.goal}>
                  {r.goal}
                </span>
              </div>
              <div className="mt-1 flex items-center gap-2 text-[10px] text-muted-foreground/70">
                <span title={r.session_id}>
                  {t('runs.sessionLabel')} {r.session_id?.slice(0, 8)}
                </span>
                <span>{relTime(r.created_at)}</span>
                {r.error_code && <span className="text-red-400">{r.error_code}</span>}
              </div>
            </div>
          ))}
          {runs.length > 0 && runs.length % 50 === 0 && (
            <button
              onClick={() => void loadMore()}
              className="w-full rounded border border-border px-2 py-1.5 text-xs text-muted-foreground hover:bg-muted hover:text-foreground"
            >
              {t('common.loadMore')}
            </button>
          )}
        </div>
      </DialogContent>
      <RunDetailDialog runId={detailRunId} onOpenChange={(v) => !v && setDetailRunId(null)} />
    </Dialog>
  )
}
