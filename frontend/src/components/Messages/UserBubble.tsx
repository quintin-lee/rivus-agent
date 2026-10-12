import type { ChatMessage } from '@/lib/chat-messages'
import { cn } from '@/lib/utils'

type UserMsg = Extract<ChatMessage, { kind: 'user' }>

export function UserBubble({ message }: { message: UserMsg }) {
  const failed = message.status === 'failed'
  return (
    <div className="flex justify-end">
      <div
        className={cn(
          'max-w-[80%] whitespace-pre-wrap rounded-2xl rounded-br-sm px-4 py-2 text-sm',
          failed
            ? 'border border-rose-500/40 bg-rose-500/10 text-rose-200'
            : 'bg-primary text-primary-foreground',
        )}
      >
        <p>{message.goal}</p>
        {message.status === 'pending' && (
          <span className="mt-1 block text-[11px] opacity-70">Sending…</span>
        )}
        {failed && (
          <span className="mt-1 block text-[11px]">
            Failed: {message.error ?? 'unknown error'}
          </span>
        )}
      </div>
    </div>
  )
}
