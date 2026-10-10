import { useState } from 'react'
import { RefreshCw, Plus, List, FolderOpen, Trash2 } from 'lucide-react'
import { useSessionStore } from '@/store/session-store'
import { useRunStore } from '@/store/run-store'
import { api } from '@/lib/api'
import { runStatusClasses, truncate, isActiveRunStatus } from '@/lib/status'
import type { Run, RunStatus } from '@/lib/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Separator } from '@/components/ui/separator'
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

export function SessionPanel() {
  const sessions = useSessionStore((s) => s.sessions)
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const createSession = useSessionStore((s) => s.createSession)
  const switchSession = useSessionStore((s) => s.switchSession)
  const loadSessions = useSessionStore((s) => s.loadSessions)
  const deleteSession = useSessionStore((s) => s.deleteSession)

  const runHistory = useRunStore((s) => s.runHistory)
  const activateRun = useRunStore((s) => s.activateRun)
  const activeRunId = useRunStore((s) => s.activeRunId)

  const [newTitle, setNewTitle] = useState('')
  const [creating, setCreating] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null)
  const [deleteRuns, setDeleteRuns] = useState<Run[]>([])
  const [deleting, setDeleting] = useState(false)
  const [deleteError, setDeleteError] = useState('')

  async function openDelete(id: string) {
    setDeleteTarget(id)
    setDeleteError('')
    setDeleteRuns([])
    try {
      setDeleteRuns(await api.listRuns(id))
    } catch {
      setDeleteError('无法读取该会话的任务列表')
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
      setDeleteError(e instanceof Error ? e.message : '删除失败')
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
            Sessions
          </div>
          <Button
            variant="ghost"
            size="icon"
            className="h-7 w-7 text-muted-foreground hover:text-foreground"
            onClick={() => void loadSessions()}
            title="Refresh sessions"
          >
            <RefreshCw className="h-3.5 w-3.5" />
          </Button>
        </div>

        <div className="flex gap-1.5">
          <Input
            value={newTitle}
            onChange={(e) => setNewTitle(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') void handleCreate()
            }}
            placeholder="New session title…"
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
            <div className="px-2 py-1 text-xs text-muted-foreground/70">No sessions</div>
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
                <button
                  onClick={() => switchSession(s.session_id)}
                  className={cn(
                    'min-w-0 flex-1 truncate rounded px-2 py-1.5 text-left text-xs',
                    active
                      ? 'text-foreground'
                      : 'text-muted-foreground hover:text-foreground',
                  )}
                >
                  {s.title || s.session_id}
                </button>
                <button
                  onClick={() => void openDelete(s.session_id)}
                  title="删除会话"
                  className="mr-1 hidden h-6 w-6 shrink-0 items-center justify-center rounded text-muted-foreground/70 hover:bg-muted hover:text-red-400 group-hover:flex"
                >
                  <Trash2 className="h-3 w-3" />
                </button>
              </div>
            )
          })}
        </div>
      </div>

      <Separator />

      <div className="flex flex-1 flex-col gap-2 overflow-y-auto p-3">
        <div className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
          <List className="h-3.5 w-3.5" />
          Run History
        </div>
        <div className="flex flex-col gap-1">
          {runHistory.length === 0 && (
            <div className="px-2 py-1 text-xs text-muted-foreground/70">No runs</div>
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
            <DialogTitle>删除会话？</DialogTitle>
            <DialogDescription>
              该会话及名下 {deleteRuns.length} 个 Run（含事件与审批记录）将被一并删除，不可恢复。
            </DialogDescription>
          </DialogHeader>
          {deleteHasActive && (
            <div className="text-xs text-amber-400">
              存在运行中的任务，请先取消后再删除。
            </div>
          )}
          {deleteError && <div className="text-xs text-red-400">{deleteError}</div>}
          <DialogFooter>
            <Button variant="ghost" onClick={() => setDeleteTarget(null)}>
              取消
            </Button>
            <Button
              variant="destructive"
              onClick={() => void confirmDelete()}
              disabled={deleting || deleteHasActive}
            >
              {deleting ? '删除中…' : '删除'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </aside>
  )
}
