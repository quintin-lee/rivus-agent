import { useMemo } from 'react'
import { deriveMessages, type ChatMessage } from '@/lib/chat-messages'
import { groupEventsByRun } from '@/store/selectors'
import { useRunStore } from '@/store/run-store'
import { UserBubble } from '@/components/Messages/UserBubble'
import { AssistantBubble } from '@/components/Messages/AssistantBubble'
import { Inbox } from 'lucide-react'
import { SystemStatusBubble } from '@/components/Messages/SystemStatusBubble'
import { SystemEventsBubble } from '@/components/Messages/SystemEventsBubble'
import { ApprovalBubble } from '@/components/Messages/ApprovalBubble'
import { SubagentCard } from '@/components/Messages/SubagentCard'
import { ToolCallsCard } from '@/components/Messages/ToolCallsCard'

export function ChatMessageList() {
  const runHistory = useRunStore((s) => s.runHistory)
  const activeRun = useRunStore((s) => s.activeRun)
  const activeSteps = useRunStore((s) => s.activeSteps)
  const events = useRunStore((s) => s.events)
  const pendingGoals = useRunStore((s) => s.pendingGoals)
  const activateRun = useRunStore((s) => s.activateRun)

  const eventsByRun = useMemo(() => groupEventsByRun(events), [events])

  const messages = useMemo(
    () =>
      deriveMessages({
        runHistory,
        activeRun,
        activeSteps,
        eventsByRun,
        pendingGoals,
        now: Date.now(),
      }),
    [runHistory, activeRun, activeSteps, eventsByRun, pendingGoals],
  )

  const handleRetry = (runId: string) => {
    void activateRun(runId)
  }
  if (messages.length === 0) {
    return (
      <div className="flex flex-col items-center gap-2 rounded-lg border border-border bg-card/50 p-8 text-center">
        <Inbox className="h-5 w-5 text-muted-foreground/60" aria-hidden />
        <p className="text-sm font-medium">No tasks yet</p>
        <p className="text-xs text-muted-foreground">Describe a task below to start this session.</p>
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-3">
      {messages.map((m) => (
        <Bubble key={m.id} message={m} onRetry={handleRetry} />
      ))}
    </div>
  )
}

function Bubble({
  message,
  onRetry,
}: {
  message: ChatMessage
  onRetry: (runId: string) => void
}) {
  switch (message.kind) {
    case 'user':
      return <UserBubble message={message} />
    case 'assistant':
      return <AssistantBubble run={message.run} />
    case 'system-status':
      return (
        <SystemStatusBubble
          run={message.run}
          steps={message.steps}
          progress={message.progress}
          partialText={message.partialText}
          onRetryRun={onRetry}
        />
      )
    case 'system-subagent':
      return <SubagentCard message={message} />
    case 'system-tools':
      return <ToolCallsCard message={message} />
    case 'system-events':
      return <SystemEventsBubble message={message} />
    case 'system-approval':
      return <ApprovalBubble message={message} />
    default:
      return null
  }
}
