import { useMemo } from 'react'
import { deriveMessages, type ChatMessage } from '@/lib/chat-messages'
import { t } from '@/i18n'
import { useUiStore } from '@/store/ui-store'
import { Button } from '@/components/ui/button'
import { groupEventsByRun } from '@/store/selectors'
import { useRunStore } from '@/store/run-store'
import type { Run } from '@/lib/types'
import { UserBubble } from '@/components/Messages/UserBubble'
import { AssistantBubble } from '@/components/Messages/AssistantBubble'
import { Bot } from 'lucide-react'
import { SystemStatusBubble } from '@/components/Messages/SystemStatusBubble'
import { SystemEventsBubble } from '@/components/Messages/SystemEventsBubble'
import { ApprovalBubble } from '@/components/Messages/ApprovalBubble'
import { SubagentCard } from '@/components/Messages/SubagentCard'
import { ToolCallsCard } from '@/components/Messages/ToolCallsCard'
import { ErrorBoundary } from '@/components/ErrorBoundary'

export function ChatMessageList() {
  const runHistory = useRunStore((s) => s.runHistory)
  const activeRun = useRunStore((s) => s.activeRun)
  const activeSteps = useRunStore((s) => s.activeSteps)
  const events = useRunStore((s) => s.events)
  const pendingGoals = useRunStore((s) => s.pendingGoals)
  const activateRun = useRunStore((s) => s.activateRun)
  const submitRun = useRunStore((s) => s.submitRun)

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
  const handleRerun = (run: Run) => {
    void submitRun({
      sessionId: run.session_id,
      goal: run.goal,
      mode: run.mode === 'plan_execute' ? 'plan_execute' : 'react',
      constraints: run.constraints,
      successCriteria: run.success_criteria,
      budget: run.budget,
    })
  }

  return (
    <ErrorBoundary>
      <div className="flex flex-col gap-3">
        {messages.length === 0 ? (
          <div className="flex flex-col items-center gap-3 rounded-lg border border-dashed border-border px-6 py-10 text-center">
            <Bot className="h-8 w-8 text-muted-foreground/60" />
            <p className="text-xs text-muted-foreground">{t('chat.empty')}</p>
            <Button size="sm" onClick={() => useUiStore.getState().setNewRunOpen(true)}>
              {t('chat.emptyCta')}
            </Button>
          </div>
        ) : (
          messages.map((m) => (
            <Bubble key={m.id} message={m} onRetry={handleRetry} onRerunRun={handleRerun} />
          ))
        )}
      </div>
    </ErrorBoundary>
  )
}

function Bubble({
  message,
  onRetry,
  onRerunRun,
}: {
  message: ChatMessage
  onRetry: (runId: string) => void
  onRerunRun: (run: Run) => void
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
          onRerunRun={onRerunRun}
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
