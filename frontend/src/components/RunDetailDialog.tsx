import { useCallback, useEffect, useState } from 'react'
import { RefreshCw } from 'lucide-react'
import { api } from '@/lib/api'
import { t } from '@/i18n'
import { useUiStore } from '@/store/ui-store'
import { runStatusClasses, stepStatusClasses } from '@/lib/status'
import type { AgentEvent, Approval, Run, Step } from '@/lib/types'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { CopyButton } from '@/components/CopyButton'

// 工具调用明细：tool.completed 事件的 payload（R5 载荷增强，args/output）。
interface ToolCallRow {
  seq: number
  tool: string
  args?: string
  output?: string
}

// 事件时间线标签：未知类型回退为原始事件类型字符串（t() 的 key 兜底语义）。
function timelineLabel(type: AgentEvent['event_type']): string {
  return t(`timeline.${type}`)
}

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

// 时间线 tool.* 行显示工具名（payload.tool，缺省时取 payload.text，与 parseToolCalls 同源）
function toolNameOf(ev: AgentEvent): string {
  if (ev.event_type !== 'tool.completed' && ev.event_type !== 'tool.failed') return ''
  try {
    const p = JSON.parse(ev.payload_json || '{}')
    const n = p?.tool ?? p?.text
    return typeof n === 'string' ? n : ''
  } catch {
    return ''
  }
}

// 时间线 step.started / step.finished 行显示该步骤描述（取 payload.step_index 与 run.steps 匹配；
// 匹配不到则省略，不做原始 step_index 回显）
function stepDescriptionOf(ev: AgentEvent, steps: Step[]): string {
  if (ev.event_type !== 'step.started' && ev.event_type !== 'step.finished') return ''
  let idx: unknown
  try {
    const p = JSON.parse(ev.payload_json || '{}')
    idx = p?.step_index
  } catch {
    return ''
  }
  if (typeof idx !== 'number' || !Number.isFinite(idx)) return ''
  const found = steps.find((s) => s.step_index === idx)
  return found?.description ?? ''
}

function parseToolCalls(events: AgentEvent[]): ToolCallRow[] {
  const rows: ToolCallRow[] = []
  for (const ev of events) {
    if (ev.event_type !== 'tool.completed') continue
    let tool = ''
    let args: string | undefined
    let output: string | undefined
    try {
      const p = JSON.parse(ev.payload_json) as Record<string, unknown>
      if (typeof p.tool === 'string') tool = p.tool
      if (typeof p.text === 'string' && !tool) tool = p.text
      if (typeof p.args === 'string') args = p.args
      if (typeof p.output === 'string') output = p.output
    } catch {
      // payload 不是对象：跳过明细，仅保留事件
    }
    rows.push({ seq: ev.seq, tool, args, output })
  }
  return rows
}

export function RunDetailDialog({
  runId,
  onOpenChange,
}: {
  runId: string | null
  onOpenChange: (v: boolean) => void
}) {
  // 订阅 lang：切换语言时触发重渲染（t() 内部读取 store 最新值）
  useUiStore((s) => s.lang)
  const [run, setRun] = useState<Run | null>(null)
  const [steps, setSteps] = useState<Step[]>([])
  const [events, setEvents] = useState<AgentEvent[]>([])
  const [approvals, setApprovals] = useState<Approval[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(false)
  const [expanded, setExpanded] = useState(false)

  const load = useCallback(async (id: string) => {
    setLoading(true)
    setError(false)
    try {
      const [r, evs, aps] = await Promise.all([
        api.getRun(id),
        api.getEvents(id, 0),
        api.listApprovals(id),
      ])
      setRun(r.run)
      setSteps(r.steps)
      setEvents(evs)
      setApprovals(aps)
    } catch {
      setError(true)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    if (!runId) return
    void load(runId)
  }, [runId, load])

  const toolCalls = parseToolCalls(events)

  return (
    <Dialog open={runId !== null} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[80vh] max-w-xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{t('rundetail.title')}</DialogTitle>
          {runId && (
            <div className="flex items-center gap-1 text-[10px] text-muted-foreground">
              <code className="truncate">{runId}</code>
              <CopyButton text={runId} />
            </div>
          )}
          <DialogDescription />
        </DialogHeader>

        {loading && <div className="text-xs text-muted-foreground">{t('common.loading')}</div>}
        {error && (
          <div className="flex items-center gap-2 text-xs text-red-400">
            <span>{t('common.loadFailed')}</span>
            <Button
              variant="ghost"
              size="sm"
              className="h-6 px-2"
              onClick={() => runId && void load(runId)}
            >
              <RefreshCw className="mr-1 h-3 w-3" />
              {t('common.retry')}
            </Button>
          </div>
        )}

        {run && !loading && !error && (
          <div className="flex flex-col gap-3 text-sm">
            <div className="flex flex-wrap items-center gap-2">
              <span
                className={`rounded px-1.5 py-0.5 text-[10px] font-medium ${runStatusClasses[run.status] ?? ''}`}
              >
                {t(`status.${run.status}`)}
              </span>
              <span className="text-sm">{run.goal}</span>
              {run.error_code && <span className="text-xs text-red-400">{run.error_code}</span>}
            </div>

            <div className="text-xs text-muted-foreground">
              {t('rundetail.budget')}:{' '}
              {t('status.budgetUsage', {
                tool: String(run.budget?.max_tool_calls ?? '-'),
                model: String(run.budget?.max_model_calls ?? '-'),
              })}
            </div>

            <div>
              <div className="mb-1 text-xs font-medium text-muted-foreground">
                {t('rundetail.steps')}
              </div>
              {steps.length === 0 ? (
                <div className="text-xs text-muted-foreground/70">{t('rundetail.emptySteps')}</div>
              ) : (
                <div className="flex flex-col gap-1">
                  {steps.map((st) => (
                    <div key={st.id} className="flex items-center gap-2 text-xs">
                      <span className="text-muted-foreground/70">#{st.step_index}</span>
                      <span className="flex-1 truncate">{st.description}</span>
                      <span
                        className={`rounded px-1.5 py-0.5 text-[10px] ${stepStatusClasses[st.status] ?? ''}`}
                      >
                        {st.status}
                      </span>
                    </div>
                  ))}
                </div>
              )}
            </div>

            <div>
              <div className="mb-1 text-xs font-medium text-muted-foreground">
                {t('rundetail.toolCalls')}
              </div>
              {toolCalls.length === 0 ? (
                <div className="text-xs text-muted-foreground/70">
                  {t('rundetail.emptyToolCalls')}
                </div>
              ) : (
                <div className="flex flex-col gap-1">
                  {toolCalls.map((tc) => (
                    <div key={tc.seq} className="rounded border border-border bg-muted/30 p-2">
                      <div className="text-xs font-medium text-teal-500">{tc.tool}</div>
                      {tc.args && (
                        <pre className="mt-1 max-h-24 overflow-y-auto whitespace-pre-wrap break-all text-[10px] text-muted-foreground">
                          {tc.args}
                        </pre>
                      )}
                      {tc.output && (
                        <pre className="mt-1 max-h-32 overflow-y-auto whitespace-pre-wrap break-all text-[10px]">
                          {tc.output}
                        </pre>
                      )}
                    </div>
                  ))}
                </div>
              )}
            </div>

            <div>
              <div className="mb-1 text-xs font-medium text-muted-foreground">
                {t('rundetail.approvals')}
              </div>
              {approvals.length === 0 ? (
                <div className="text-xs text-muted-foreground/70">
                  {t('rundetail.emptyApprovals')}
                </div>
              ) : (
                <div className="flex flex-col gap-1">
                  {approvals.map((ap) => (
                    <div key={ap.id} className="flex items-center gap-2 text-xs">
                      <span className="font-medium">{ap.tool_name}</span>
                      <span className="text-muted-foreground">{ap.status}</span>
                      {ap.reason && (
                        <span className="truncate text-muted-foreground/70">{ap.reason}</span>
                      )}
                    </div>
                  ))}
                </div>
              )}
            </div>

            <div>
              <div className="mb-1 flex items-center gap-2 text-xs font-medium text-muted-foreground">
                {t('timeline.title')}
                {events.length > 0 && (
                  <span className="font-normal text-muted-foreground/70">
                    {t('timeline.eventCount', { n: events.length })}
                  </span>
                )}
              </div>
              {events.length === 0 ? (
                <div className="text-xs text-muted-foreground/70">{t('timeline.empty')}</div>
              ) : (
                <div className="relative flex flex-col gap-2 pl-4">
                  <span className="absolute left-1.5 top-1 h-[calc(100%-0.5rem)] w-px bg-border" />
                  {(expanded ? events : events.slice(-50)).map((ev) => (
                    <div key={ev.seq} className="relative flex items-center gap-2 text-xs">
                      <span className="absolute -left-4 top-1/2 h-3 w-3 -translate-y-1/2 rounded-full border border-border bg-card" />
                      <span className="font-medium">{timelineLabel(ev.event_type)}</span>
                      {toolNameOf(ev) && (
                        <code className="rounded bg-muted px-1 text-[10px] text-muted-foreground">
                          {toolNameOf(ev)}
                        </code>
                      )}
                      {stepDescriptionOf(ev, steps) && (
                        <span className="truncate text-muted-foreground">{stepDescriptionOf(ev, steps)}</span>
                      )}
                      <span className="text-muted-foreground/70">{relTime(ev.created_at)}</span>
                    </div>
                  ))}
                  {events.length > 50 && (
                    <button
                      type="button"
                      onClick={() => setExpanded((v) => !v)}
                      className="ml-auto text-xs text-primary hover:underline"
                    >
                      {expanded ? t('timeline.collapse') : t('timeline.expandAll', { n: events.length })}
                    </button>
                  )}
                </div>
              )}
            </div>
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}
