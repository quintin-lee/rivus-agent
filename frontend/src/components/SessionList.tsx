import { useEffect, useRef, useState } from 'react'
import { Plus, FolderOpen, Trash2, RefreshCw, Brain, Pencil, Search } from 'lucide-react'
import { useSessionStore } from '@/store/session-store'
import { useRunStore } from '@/store/run-store'
import { api } from '@/lib/api'
import { runStatusClasses, truncate, isActiveRunStatus } from '@/lib/status'
import { t } from '@/i18n'
import { useUiStore } from '@/store/ui-store'
import type { Run, RunStatus } from '@/lib/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { MemoriesDialog } from '@/components/MemoriesDialog'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { cn } from '@/lib/utils'

const runDot: Record<RunStatus, string> = {
  queued: runStatusClasses.queued,
  running: runStatusClasses.running,
  waiting_approval: runStatusClasses.waiting_approval,
  paused: runStatusClasses.paused,
  succeeded: runStatusClasses.succeeded,
  failed: runStatusClasses.failed,
  cancelled: runStatusClasses.cancelled,
  timed_out: runStatusClasses.timed_out,
}

export function SessionList() {
  const sessions = useSessionStore((s) => s.sessions)
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const createSession = useSessionStore((s) => s.createSession)
  const switchSession = useSessionStore((s) => s.switchSession)
  const loadSessions = useSessionStore((s) => s.loadSessions)
  const loadMoreSessions = useSessionStore((s) => s.loadMoreSessions)
  const deleteSession = useSessionStore((s) => s.deleteSession)
  const renameSession = useSessionStore((s) => s.renameSession)

  const runHistory = useRunStore((s) => s.runHistory)
  const activateRun = useRunStore((s) => s.activateRun)
  const activeRunId = useRunStore((s) => s.activeRunId)

  // 订阅 lang：切换语言时触发重渲染（t() 内部读取 store 最新值）
  useUiStore((s) => s.lang)

  const [newTitle, setNewTitle] = useState('')
  const [creating, setCreating] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null)
  const [deleteRuns, setDeleteRuns] = useState<Run[]>([])
  const [deleting, setDeleting] = useState(false)
  const [deleteError, setDeleteError] = useState('')
  const [memoriesTarget, setMemoriesTarget] = useState<{ id: string; title: string } | null>(null)
  const [renamingId, setRenamingId] = useState<string | null>(null)
  const [renameDraft, setRenameDraft] = useState('')
  const [searchDraft, setSearchDraft] = useState('')
  const [query, setQuery] = useState('')

  // 搜索防抖：输入停止 200ms 后才更新 query
  useEffect(() => {
    const id = setTimeout(() => setQuery(searchDraft), 200)
    return () => clearTimeout(id)
  }, [searchDraft])

  // query 变化触发带 q 的重新拉取（跳过挂载首跑——App 已做初始全量加载）
  const firstRun = useRef(true)
  useEffect(() => {
    if (firstRun.current) {
      firstRun.current = false
      return
    }
    void loadSessions(query || undefined)
  }, [query, loadSessions])

  function startRename(id: string, title: string) {
    setRenamingId(id)
    setRenameDraft(title)
  }

  async function commitRename() {
    if (!renamingId) return
    const title = renameDraft.trim()
    setRenamingId(null)
    if (!title) return // 空标题视为取消
    await renameSession(renamingId, title)
  }

  async function openDelete(id: string) {
    setDeleteTarget(id)
    setDeleteError('')
    setDeleteRuns([])
    try {
      setDeleteRuns(await api.listRuns(id))
    } catch {
      setDeleteError(t('sessionlist.loadRunsFailed'))
    }
  }

  async function confirmDelete() {
    if (!deleteTarget || deleting) return
    setDeleting(true)
    setDeleteError('')
    try {
      await deleteSession(deleteTarget)
      setDeleteTarget(null)
    } catch (e) {
      setDeleteError(e instanceof Error ? e.message : t('common.deleteFailed'))
    } finally {
      setDeleting(false)
    }
  }

  const deleteHasActive = deleteRuns.some((r) => isActiveRunStatus(r.status))

  async function handleCreate() {
    const title = newTitle.trim()
    if (!title || creating) return
    setCreating(true)
    try {
      await createSession(title)
      setNewTitle('')
    } finally {
      setCreating(false)
    }
  }

  return (
    <aside className="flex w-60 shrink-0 flex-col border-r border-border bg-card">
      <div className="flex flex-col gap-2 p-3">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
            <FolderOpen className="h-3.5 w-3.5" />
            {t('sessionlist.title')}
          </div>
          <Button
            variant="ghost"
            size="icon"
            className="h-7 w-7 text-muted-foreground hover:text-foreground"
            onClick={() => void loadSessions(query || undefined)}
            title={t('sessionlist.refresh')}
          >
            <RefreshCw className="h-3.5 w-3.5" />
          </Button>
        </div>

        <div className="relative">
          <Search className="absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={searchDraft}
            onChange={(e) => setSearchDraft(e.target.value)}
            placeholder={t('sessionlist.searchPlaceholder')}
            className="h-8 pl-7 text-xs"
          />
        </div>

        <div className="flex gap-1.5">
          <Input
            value={newTitle}
            onChange={(e) => setNewTitle(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') void handleCreate()
            }}
            placeholder={t('sessionlist.newPlaceholder')}
            className="h-8 text-xs"
          />
          <Button
            size="sm"
            className="h-8 px-2"
            onClick={() => void handleCreate()}
            disabled={!newTitle.trim() || creating}
          >
            <Plus className="h-3.5 w-3.5" />
          </Button>
        </div>

        <div className="flex flex-col gap-0.5">
          {sessions.length === 0 && (
            <div className="px-2 py-1 text-xs text-muted-foreground/70">
              {t('sessionlist.noSessions')}
            </div>
          )}
          {sessions.map((s) => {
            const active = s.session_id === activeSessionId
            return (
              <div
                key={s.session_id}
                className={cn(
                  'group flex items-center gap-1 rounded',
                  active ? 'bg-muted' : 'hover:bg-muted/50',
                )}
              >
                {renamingId === s.session_id ? (
                  <Input
                    autoFocus
                    value={renameDraft}
                    onChange={(e) => setRenameDraft(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter') void commitRename()
                      if (e.key === 'Escape') setRenamingId(null)
                    }}
                    onBlur={() => void commitRename()}
                    className="h-6 text-xs"
                  />
                ) : (
                  <button
                    onClick={() => switchSession(s.session_id)}
                    className={cn(
                      'min-w-0 flex-1 truncate rounded px-2 py-1.5 text-left text-xs',
                      active
                        ? 'font-medium text-foreground'
                        : 'text-muted-foreground hover:text-foreground',
                    )}
                  >
                    {s.title || s.session_id}
                  </button>
                )}
                <button
                  onClick={() => startRename(s.session_id, s.title || s.session_id)}
                  title={t('sessionlist.renameTitle')}
                  className="hidden h-6 w-6 shrink-0 items-center justify-center rounded text-muted-foreground/70 hover:bg-muted hover:text-foreground group-hover:flex"
                >
                  <Pencil className="h-3 w-3" />
                </button>
                <button
                  onClick={() => setMemoriesTarget({ id: s.session_id, title: s.title })}
                  title={t('sessionlist.memoriesTitle')}
                  className="hidden h-6 w-6 shrink-0 items-center justify-center rounded text-muted-foreground/70 hover:bg-muted hover:text-foreground group-hover:flex"
                >
                  <Brain className="h-3 w-3" />
                </button>
                <button
                  onClick={() => void openDelete(s.session_id)}
                  title={t('sessionlist.deleteTitle')}
                  className="mr-1 hidden h-6 w-6 shrink-0 items-center justify-center rounded text-muted-foreground/70 hover:bg-muted hover:text-red-400 group-hover:flex"
                >
                  <Trash2 className="h-3 w-3" />
                </button>
              </div>
            )
          })}
          {sessions.length > 0 && sessions.length % 50 === 0 && (
            <button
              onClick={() => void loadMoreSessions(query || undefined)}
              className="w-full rounded px-2 py-1 text-xs text-muted-foreground hover:bg-muted hover:text-foreground"
            >
              {t('common.loadMore')}
            </button>
          )}
        </div>
      </div>

      <div className="flex-1 overflow-y-auto p-3 pt-0">
        <div className="flex flex-col gap-1">
          {runHistory.length === 0 && (
            <div className="px-2 py-1 text-xs text-muted-foreground/70">
              {t('sessionlist.noRuns')}
            </div>
          )}
          {runHistory.map((run) => {
            const active = run.id === activeRunId
            return (
              <button
                key={run.id}
                onClick={() => void activateRun(run.id)}
                className={cn(
                  'flex flex-col gap-1 rounded border p-2 text-left',
                  active
                    ? 'border-border bg-muted/70'
                    : 'border-transparent hover:border-border hover:bg-muted/40',
                )}
              >
                <span className="truncate text-xs text-foreground">
                  {truncate(run.goal || run.id, 40)}
                </span>
                <span
                  className={cn(
                    'inline-flex w-fit border rounded-full px-1.5 py-0.5 text-[10px]',
                    runDot[run.status],
                  )}
                >
                  {run.status}
                </span>
              </button>
            )
          })}
        </div>
      </div>

      <Dialog open={deleteTarget !== null} onOpenChange={(v) => !v && setDeleteTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('sessionlist.deleteConfirmTitle')}</DialogTitle>
            <DialogDescription>
              {t('sessionlist.deleteConfirmBody', { count: deleteRuns.length })}
            </DialogDescription>
          </DialogHeader>
          {deleteHasActive && (
            <div className="text-xs text-amber-400">{t('sessionlist.deleteActiveWarn')}</div>
          )}
          {deleteError && <div className="text-xs text-red-400">{deleteError}</div>}
          <DialogFooter>
            <Button variant="ghost" onClick={() => setDeleteTarget(null)}>
              {t('common.cancel')}
            </Button>
            <Button
              variant="destructive"
              onClick={() => void confirmDelete()}
              disabled={deleting || deleteHasActive}
            >
              {deleting ? t('common.deleting') : t('common.delete')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <MemoriesDialog
        sessionId={memoriesTarget?.id ?? ''}
        sessionTitle={memoriesTarget?.title ?? ''}
        open={memoriesTarget !== null}
        onOpenChange={(v) => !v && setMemoriesTarget(null)}
      />
    </aside>
  )
}
