import { useEffect, useMemo, useRef, useState } from 'react'
import { Search, ArrowDown, ListFilter } from 'lucide-react'
import { useRunStore } from '@/store/run-store'
import { useUiStore } from '@/store/ui-store'
import type { AgentEvent, EventType } from '@/lib/types'
import {
  eventCategory,
  eventCategoryClasses,
  formatTimestamp,
} from '@/lib/status'
import { ApprovalCard } from '@/components/ApprovalCard'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'

type Filter = EventType | 'all'

// Each chip maps to a representative event type; filtering compares categories.
const FILTER_CHIPS: { value: Filter; label: string }[] = [
  { value: 'all', label: 'all' },
  { value: 'run.created', label: 'run' },
  { value: 'model.requested', label: 'model' },
  { value: 'tool.requested', label: 'tool' },
  { value: 'step.started', label: 'step' },
  { value: 'approval.requested', label: 'approval' },
]

function chipCategory(chip: Filter): string {
  if (chip === 'all') return 'all'
  return eventCategory(chip)
}

function payload(e: AgentEvent): Record<string, unknown> {
  try {
    return JSON.parse(e.payload_json || '{}')
  } catch {
    return {}
  }
}

function summaryFor(e: AgentEvent): string {
  const p = payload(e)
  switch (e.event_type) {
    case 'tool.requested':
    case 'tool.started':
    case 'tool.completed':
    case 'tool.failed':
    case 'tool.authorized':
    case 'tool.denied':
      return p.tool_name ? `tool: ${String(p.tool_name)}` : e.event_type
    case 'model.requested':
    case 'model.completed':
    case 'model.failed':
      return p.call_count !== undefined
        ? `model call ${Number(p.call_count)}`
        : e.event_type
    case 'step.started':
    case 'step.finished':
      return p.description ? String(p.description) : `step ${p.step_index ?? ''}`
    case 'approval.requested':
      return p.tool_name ? `approval: ${String(p.tool_name)}` : 'approval requested'
    case 'approval.decided':
      return p.approved ? 'approval granted' : 'approval denied'
    case 'run.created':
      return 'run created'
    case 'run.started':
      return 'run started'
    case 'run.finished':
      return 'run finished'
    case 'run.failed':
      return p.error_code ? `run failed: ${String(p.error_code)}` : 'run failed'
    case 'run.cancelled':
      return 'run cancelled'
    case 'run.resumed':
      return 'run resumed'
    case 'plan.created':
      return 'plan created'
    case 'plan.updated':
      return 'plan updated'
    case 'checkpoint.saved':
      return 'checkpoint saved'
    case 'verification.passed':
      return 'verification passed'
    case 'verification.failed':
      return 'verification failed'
    default:
      return e.event_type
  }
}

export function EventStream() {
  const events = useRunStore((s) => s.events)
  const eventFilter = useRunStore((s) => s.eventFilter)
  const setEventFilter = useRunStore((s) => s.setEventFilter)

  const eventStreamOpen = useUiStore((s) => s.eventStreamOpen)
  const toggleEventStream = useUiStore((s) => s.toggleEventStream)

  const [query, setQuery] = useState('')
  const [scrolledUp, setScrolledUp] = useState(false)
  const listRef = useRef<HTMLDivElement>(null)

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    return events.filter((e) => {
      if (eventFilter !== 'all' && eventCategory(e.event_type) !== chipCategory(eventFilter)) {
        return false
      }
      if (q && !e.payload_json.toLowerCase().includes(q)) return false
      return true
    })
  }, [events, eventFilter, query])

  // Auto-scroll to bottom on new events if the user is at the bottom.
  useEffect(() => {
    const el = listRef.current
    if (!el) return
    if (!scrolledUp) {
      el.scrollTop = el.scrollHeight
    }
  }, [filtered, scrolledUp])

  if (!eventStreamOpen) return null

  function onScroll() {
    const el = listRef.current
    if (!el) return
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 40
    setScrolledUp(!atBottom)
  }

  function jumpToBottom() {
    const el = listRef.current
    if (el) el.scrollTop = el.scrollHeight
    setScrolledUp(false)
  }

  function pickFilter(chip: Filter) {
    setEventFilter(chip)
  }

  return (
    <section
      id="event-stream"
      className="flex min-h-0 flex-col rounded-lg border border-border bg-background"
    >
      <div className="flex flex-wrap items-center gap-2 border-b border-border px-3 py-2">
        <ListFilter className="h-4 w-4 text-muted-foreground/70" />
        <div className="flex items-center gap-1">
          {FILTER_CHIPS.map((chip) => {
            const active = eventFilter === chip.value
            return (
              <button
                key={chip.value}
                onClick={() => pickFilter(chip.value)}
                className={cn(
                  'rounded-full px-2 py-0.5 text-xs font-medium',
                  active
                    ? 'bg-primary text-primary-foreground'
                    : 'bg-card text-muted-foreground hover:text-foreground',
                )}
              >
                {chip.label}
              </button>
            )
          })}
        </div>
        <div className="ml-auto flex items-center gap-2">
          <div className="relative">
            <Search className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground/70" />
            <Input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Search…"
              className="h-7 w-40 pl-7 text-xs"
            />
          </div>
          <button
            onClick={toggleEventStream}
            className="rounded px-2 py-0.5 text-xs text-muted-foreground hover:text-foreground"
          >
            close
          </button>
        </div>
      </div>

      <div className="relative min-h-0 flex-1">
        <div
          ref={listRef}
          onScroll={onScroll}
          className="h-full max-h-[400px] overflow-y-auto p-3 font-mono text-xs"
        >
          {filtered.length === 0 && (
            <div className="text-muted-foreground/50">No events</div>
          )}
          {filtered.map((e) => {
            const masked = e.sensitivity === 'high'
            const cat = eventCategory(e.event_type)
            return (
              <div key={`${e.seq}-${e.event_type}`} className="py-0.5">
                <div className="flex items-center gap-2 text-muted-foreground">
                  <span className="text-muted-foreground/50">{formatTimestamp(e.created_at)}</span>
                  <Badge variant="outline" className={cn('shrink-0 text-[10px]', eventCategoryClasses[cat])}>
                    {e.event_type}
                  </Badge>
                  <span className="truncate text-foreground/90">
                    {masked ? (
                      <span className="italic text-muted-foreground/70">[masked]</span>
                    ) : (
                      summaryFor(e)
                    )}
                  </span>
                </div>
                {e.event_type === 'approval.requested' && !masked && (
                  <ApprovalCard event={e} />
                )}
              </div>
            )
          })}
        </div>

        {scrolledUp && (
          <button
            onClick={jumpToBottom}
            className="absolute bottom-2 right-2 flex items-center gap-1 rounded-full border border-border bg-muted px-2.5 py-1 text-xs text-foreground shadow hover:bg-muted/80"
          >
            <ArrowDown className="h-3 w-3" />
            New
          </button>
        )}
      </div>
    </section>
  )
}
