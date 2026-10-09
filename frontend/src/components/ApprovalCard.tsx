import { useEffect, useMemo, useState } from 'react'
import { Check, X, Clock, ShieldCheck } from 'lucide-react'
import type { AgentEvent } from '@/lib/types'
import { useRunStore } from '@/store/run-store'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Textarea } from '@/components/ui/textarea'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
  DialogDescription,
} from '@/components/ui/dialog'

interface ParsedApproval {
  approvalId: string
  toolName: string
  expiresAt: number
}

function parseApproval(event: AgentEvent): ParsedApproval {
  let approvalId = ''
  let toolName = ''
  let expiresAt = 0
  try {
    const p = JSON.parse(event.payload_json || '{}')
    approvalId = p.approval_id ?? p.approvalId ?? ''
    toolName = p.tool_name ?? p.toolName ?? ''
    expiresAt = Number(p.expires_at ?? p.expiresAt ?? 0)
  } catch {
    // leave defaults
  }
  return { approvalId, toolName, expiresAt }
}

const decisionBadge: Record<string, string> = {
  approved: 'bg-emerald-500/15 text-emerald-400 border-emerald-500/30',
  rejected: 'bg-red-500/15 text-red-400 border-red-500/30',
  expired: 'bg-zinc-700 text-zinc-400 border-zinc-600',
}

export function ApprovalCard({ event }: { event: AgentEvent }) {
  const fetchRunAndSteps = useRunStore((s) => s.fetchRunAndSteps)

  const parsed = useMemo(() => parseApproval(event), [event.payload_json])
  const [decided, setDecided] = useState<'approved' | 'rejected' | 'expired' | null>(null)
  const [rejectOpen, setRejectOpen] = useState(false)
  const [reason, setReason] = useState('')
  const [busy, setBusy] = useState(false)
  const [now, setNow] = useState(() => Date.now())

  const runId = event.run_id
  const approvalId = parsed.approvalId

  useEffect(() => {
    if (!parsed.expiresAt || decided) return
    const id = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(id)
  }, [parsed.expiresAt, decided])

  // Mark expired if the deadline has passed and we haven't decided.
  useEffect(() => {
    if (decided) return
    if (parsed.expiresAt && now > parsed.expiresAt) {
      setDecided('expired')
    }
  }, [now, parsed.expiresAt, decided])

  if (!approvalId) return null

  const remaining = parsed.expiresAt ? Math.max(0, Math.floor((parsed.expiresAt - now) / 1000)) : null

  async function approve() {
    if (decided || busy) return
    setBusy(true)
    try {
      await api.decideApproval(runId, approvalId, true)
      setDecided('approved')
      await fetchRunAndSteps(runId)
    } catch {
      // keep local decided state; user can retry
    } finally {
      setBusy(false)
    }
  }

  async function reject() {
    if (decided || busy) return
    setBusy(true)
    try {
      await api.decideApproval(runId, approvalId, false, reason.trim() || undefined)
      setDecided('rejected')
      setRejectOpen(false)
      await fetchRunAndSteps(runId)
    } catch {
      // keep local decided state; user can retry
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="mt-2 rounded-lg border border-amber-500/30 bg-amber-500/5 p-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2 text-sm">
          <ShieldCheck className="h-4 w-4 text-amber-400" />
          <span className="font-medium text-zinc-100">
            Approval required{parsed.toolName ? ` · ${parsed.toolName}` : ''}
          </span>
        </div>
        {decided ? (
          <Badge variant="outline" className={decisionBadge[decided]}>
            {decided}
          </Badge>
        ) : remaining !== null ? (
          <span className="flex items-center gap-1 text-xs text-amber-400">
            <Clock className="h-3.5 w-3.5" />
            {remaining}s left
          </span>
        ) : null}
      </div>

      {!decided && (
        <div className="mt-3 flex items-center gap-2">
          <Button
            size="sm"
            className="bg-emerald-600 hover:bg-emerald-500"
            onClick={() => void approve()}
            disabled={busy}
          >
            <Check className="h-3.5 w-3.5" />
            Approve
          </Button>
          <Button
            size="sm"
            variant="outline"
            className="border-red-500/40 text-red-400 hover:bg-red-500/10 hover:text-red-300"
            onClick={() => setRejectOpen(true)}
            disabled={busy}
          >
            <X className="h-3.5 w-3.5" />
            Reject
          </Button>
        </div>
      )}

      <Dialog open={rejectOpen} onOpenChange={setRejectOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Reject approval</DialogTitle>
            <DialogDescription>
              Provide a reason so the agent can adjust its plan.
            </DialogDescription>
          </DialogHeader>
          <Textarea
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            placeholder="Why should this action be rejected?"
            rows={3}
          />
          <DialogFooter>
            <Button variant="ghost" onClick={() => setRejectOpen(false)}>
              Cancel
            </Button>
            <Button variant="destructive" onClick={() => void reject()} disabled={busy}>
              {busy ? 'Rejecting…' : 'Reject'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
