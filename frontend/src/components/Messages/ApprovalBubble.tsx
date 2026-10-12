import { api } from '@/lib/api'
import { useRunStore } from '@/store/run-store'
import { ApprovalCard } from '@/components/ApprovalCard'
import type { ChatMessage } from '@/lib/chat-messages'

type ApprovalMsg = Extract<ChatMessage, { kind: 'system-approval' }>

export function ApprovalBubble({ message }: { message: ApprovalMsg }) {
  const fetchRunAndSteps = useRunStore((s) => s.fetchRunAndSteps)

  if (message.state === 'pending') {
    return (
      <ApprovalCard
        event={message.event}
        state="pending"
        onApprove={() => {
          void api
            .decideApproval(message.runId, approvalId(message.event), true)
            .then(() => fetchRunAndSteps(message.runId))
            .catch(() => {})
        }}
        onReject={(reason) => {
          void api
            .decideApproval(message.runId, approvalId(message.event), false, reason)
            .then(() => fetchRunAndSteps(message.runId))
            .catch(() => {})
        }}
      />
    )
  }
  return (
    <ApprovalCard event={message.event} state={message.state} onApprove={() => {}} onReject={() => {}} />
  )
}

function approvalId(event: ApprovalMsg['event']): string {
  try {
    const p = JSON.parse(event.payload_json || '{}')
    return (p.approval_id as string) ?? (p.approvalId as string) ?? ''
  } catch {
    return ''
  }
}
