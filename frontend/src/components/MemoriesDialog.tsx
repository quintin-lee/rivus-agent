import { useCallback, useEffect, useState } from 'react'
import { Brain, RefreshCw } from 'lucide-react'
import { api } from '@/lib/api'
import { t } from '@/i18n'
import { useUiStore } from '@/store/ui-store'
import type { Memory } from '@/lib/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
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

export function MemoriesDialog({
  sessionId,
  sessionTitle,
  open,
  onOpenChange,
}: {
  sessionId: string
  sessionTitle: string
  open: boolean
  onOpenChange: (v: boolean) => void
}) {
  // 订阅 lang：切换语言时触发重渲染（t() 内部读取 store 最新值）
  useUiStore((s) => s.lang)
  const [memories, setMemories] = useState<Memory[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [filter, setFilter] = useState('')

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      setMemories(await api.listMemories(sessionId))
    } catch (e) {
      setError(e instanceof Error ? e.message : t('common.loadFailed'))
    } finally {
      setLoading(false)
    }
  }, [sessionId])

  useEffect(() => {
    if (open) void load()
  }, [open, load])

  const kw = filter.trim().toLowerCase()
  const shown = kw ? memories.filter((m) => m.content.toLowerCase().includes(kw)) : memories

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[70vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-1.5">
            <Brain className="h-4 w-4" />
            {t('memories.title')}
            {sessionTitle ? ` · ${sessionTitle}` : ''}
          </DialogTitle>
          <DialogDescription>{t('memories.description')}</DialogDescription>
        </DialogHeader>
        <div className="flex items-center gap-2">
          <Input
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder={t('memories.filterPlaceholder')}
            className="h-8 text-xs"
          />
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
        {!loading && !error && shown.length === 0 && (
          <div className="text-xs text-muted-foreground/70">{t('memories.empty')}</div>
        )}
        <div className="flex flex-col gap-2">
          {shown.map((m) => (
            <div key={m.id} className="rounded border border-border bg-muted/30 p-2">
              <div className="whitespace-pre-wrap text-xs text-foreground">{m.content}</div>
              <div className="mt-1 flex items-center gap-2 text-[10px] text-muted-foreground/70">
                <span>{relTime(m.created_at)}</span>
                {m.source_run_id && (
                  <span title={m.source_run_id}>run {m.source_run_id.slice(0, 8)}</span>
                )}
              </div>
            </div>
          ))}
        </div>
      </DialogContent>
    </Dialog>
  )
}
