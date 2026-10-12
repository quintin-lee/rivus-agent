import { useState } from 'react'
import { api, ApiError } from '@/lib/api'
import { useRunStore } from '@/store/run-store'
import { ApprovalCard } from '@/components/ApprovalCard'
import type { ChatMessage } from '@/lib/chat-messages'

type ApprovalMsg = Extract<ChatMessage, { kind: 'system-approval' }>

export function ApprovalBubble({ message }: { message: ApprovalMsg }) {
  const fetchRunAndSteps = useRunStore((s) => s.fetchRunAndSteps)
  const [error, setError] = useState<string | null>(null)

  const handleApprove = async () => {
    setError(null)
    try {
      await api.decideApproval(message.runId, approvalId(message.event), true)
      await fetchRunAndSteps(message.runId)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Failed to approve')
    }
  }

  const handleReject = async (reason: string) => {
    setError(null)
    try {
      await api.decideApproval(message.runId, approvalId(message.event), false, reason)
      await fetchRunAndSteps(message.runId)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Failed to reject')
    }
  }

  return (
    <div>
      <ApprovalCard
        event={message.event}
        state={message.state === 'pending' ? 'pending' : message.state}
        onApprove={() => {
          void handleApprove()
        }}
        onReject={(reason) => {
          void handleReject(reason)
        }}
      />
      {error && <p className="mt-1 text-xs text-rose-600 dark:text-rose-400">{error}</p>}
    </div>
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
