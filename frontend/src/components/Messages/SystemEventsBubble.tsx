import { useState } from 'react'
import type { ChatMessage } from '@/lib/chat-messages'
import {
  eventCategory,
  eventCategoryClasses,
  eventSummary,
  formatTimestamp,
  truncate,
} from '@/lib/status'
import { cn } from '@/lib/utils'

type EventsMsg = Extract<ChatMessage, { kind: 'system-events' }>

export function SystemEventsBubble({ message }: { message: EventsMsg }) {
  return (
    <details
      className="rounded-lg border border-border bg-card/40 text-xs"
      open={message.collapsed ? false : true}
    >
      <summary className="cursor-pointer select-none px-3 py-2 text-[11px] font-medium text-muted-foreground hover:text-foreground">
        {message.events.length} events
      </summary>
      <ol className="divide-y divide-border/50">
        {message.events.map((e, i) => (
          <EventRow key={`${e.seq}-${i}`} event={e} />
        ))}
      </ol>
    </details>
  )
}

function EventRow({
  event,
}: {
  event: EventsMsg['events'][number]
}) {
  const [showRaw, setShowRaw] = useState(false)

  return (
    <li className="px-3 py-1.5">
      <div className="flex items-center gap-2">
        <span
          className={cn(
            'rounded-full px-1.5 py-0.5 text-[10px]',
            eventCategoryClasses[eventCategory(event.event_type)],
          )}
        >
          {event.event_type.replace('.', ' ')}
        </span>
        <span className="flex-1 text-muted-foreground">
          {event.sensitivity === 'high'
            ? '[masked]'
            : truncate(eventSummary(event), 80)}
        </span>
        <button
          type="button"
          className="text-[10px] text-muted-foreground/50 hover:text-foreground"
          onClick={() => setShowRaw((v) => !v)}
        >
          {showRaw ? 'hide' : 'raw'}
        </button>
        <span className="text-[10px] text-muted-foreground/60">
          {formatTimestamp(event.created_at)}
        </span>
      </div>
      {showRaw && (
        <pre className="mt-1 max-h-40 overflow-auto rounded bg-muted/50 p-2 text-[10px] text-foreground/80">
          {event.payload_json}
        </pre>
      )}
    </li>
  )
}
