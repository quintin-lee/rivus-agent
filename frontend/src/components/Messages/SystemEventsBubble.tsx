import type { ChatMessage } from '@/lib/chat-messages'
import { eventCategory, eventCategoryClasses, formatTimestamp, truncate } from '@/lib/status'
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
          <li key={`${e.seq}-${i}`} className="flex items-center gap-2 px-3 py-1.5">
            <span
              className={cn(
                'rounded-full px-1.5 py-0.5 text-[10px]',
                eventCategoryClasses[eventCategory(e.event_type)],
              )}
            >
              {e.event_type.replace('.', ' ')}
            </span>
            <span className="flex-1 truncate text-muted-foreground">
              {truncate(e.payload_json || '', 80)}
            </span>
            <span className="text-[10px] text-muted-foreground/60">{formatTimestamp(e.created_at)}</span>
          </li>
        ))}
      </ol>
    </details>
  )
}
