import { useState } from 'react'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog'

interface Props {
  event: import('@/lib/types').AgentEvent
  state: 'pending' | 'approved' | 'rejected' | 'expired'
  onApprove: () => void
  onReject: (reason: string) => void
}

function parseApproval(event: Props['event']) {
  let p: Record<string, unknown> = {}
  try {
    p = JSON.parse(event.payload_json || '{}')
  } catch {
    p = {}
  }
  return {
    approvalId: (p.approval_id as string) ?? (p.approvalId as string) ?? '',
    toolName: (p.tool_name as string) ?? (p.toolName as string) ?? 'tool',
  }
}

const BADGE: Record<Props['state'], { cls: string; label: string }> = {
  pending: { cls: 'bg-amber-500/15 text-amber-600 dark:text-amber-400', label: 'Pending' },
  approved: { cls: 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400', label: 'Approved' },
  rejected: { cls: 'bg-rose-500/15 text-rose-600 dark:text-rose-400', label: 'Rejected' },
  expired: { cls: 'bg-muted text-muted-foreground', label: 'Expired' },
}

export function ApprovalCard({ event, state, onApprove, onReject }: Props) {
  const { approvalId, toolName } = parseApproval(event)
  const [rejectOpen, setRejectOpen] = useState(false)
  const [reason, setReason] = useState('')

  if (!approvalId) return null

  const badge = BADGE[state]
  const resolved = state === 'approved' || state === 'rejected' || state === 'expired'
  if (resolved) {
    const line =
      state === 'approved'
        ? `✓ Approved ${toolName}`
        : state === 'rejected'
          ? `✗ Rejected ${toolName}`
          : `Approval expired · ${toolName}`
    return <p className="text-xs text-muted-foreground">{line}</p>
  }

  return (
    <div className="rounded-lg border border-border bg-card p-3">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2 text-sm">
          <span className="font-medium">Tool approval</span>
          <span className="text-muted-foreground">·</span>
          <code className="text-xs">{toolName}</code>
        </div>
        <span className={`rounded-full px-2 py-0.5 text-[11px] ${badge.cls}`}>{badge.label}</span>
      </div>
      {!resolved && (
        <div className="mt-3 flex items-center gap-2">
          <button
            type="button"
            className="rounded-md bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:bg-primary/90"
            onClick={() => onApprove()}
          >
            Approve
          </button>
          <button
            type="button"
            className="rounded-md border border-border px-3 py-1.5 text-xs font-medium hover:bg-accent"
            onClick={() => setRejectOpen(true)}
          >
            Reject
          </button>
          <Dialog open={rejectOpen} onOpenChange={setRejectOpen}>
            <DialogContent className="sm:max-w-[420px]">
              <DialogHeader>
                <DialogTitle>Reject approval</DialogTitle>
              </DialogHeader>
              <textarea
                className="w-full rounded-md border border-border bg-background p-2 text-sm"
                rows={3}
                placeholder="Reason (optional)"
                value={reason}
                onChange={(e) => setReason(e.target.value)}
              />
              <DialogFooter>
                <button
                  type="button"
                  className="rounded-md border border-border px-3 py-1.5 text-xs hover:bg-accent"
                  onClick={() => setRejectOpen(false)}
                >
                  Cancel
                </button>
                <button
                  type="button"
                  className="rounded-md bg-rose-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-rose-700"
                  onClick={() => {
                    setRejectOpen(false)
                    onReject(reason.trim())
                  }}
                >
                  Confirm
                </button>
              </DialogFooter>
            </DialogContent>
          </Dialog>
        </div>
      )}
    </div>
  )
}
