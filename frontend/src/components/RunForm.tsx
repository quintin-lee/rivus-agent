import { useState } from 'react'
import { Plus, Minus, ChevronDown, ChevronRight, Rocket } from 'lucide-react'
import { useSessionStore } from '@/store/session-store'
import { useRunStore } from '@/store/run-store'
import { api } from '@/lib/api'
import type { CreateRunReq } from '@/lib/types'
import { Card, CardHeader, CardContent } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Separator } from '@/components/ui/separator'
import { cn } from '@/lib/utils'

type Mode = 'react' | 'plan_execute'

export function RunForm() {
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const activeRun = useRunStore((s) => s.activeRun)
  const activateRun = useRunStore((s) => s.activateRun)

  const [goal, setGoal] = useState('')
  const [mode, setMode] = useState<Mode>('react')
  const [constraints, setConstraints] = useState<string[]>([])
  const [successCriteria, setSuccessCriteria] = useState<string[]>([])
  const [newConstraint, setNewConstraint] = useState('')
  const [newCriterion, setNewCriterion] = useState('')
  const [showAdvanced, setShowAdvanced] = useState(false)
  const [submitting, setSubmitting] = useState(false)

  // If a run is already active, collapse to just the header.
  if (activeRun) {
    return (
      <Card>
        <CardHeader className="flex flex-row items-center justify-between py-3">
          <div className="text-sm font-semibold">New Run</div>
        </CardHeader>
      </Card>
    )
  }

  function addConstraint() {
    const v = newConstraint.trim()
    if (!v) return
    setConstraints((prev) => [...prev, v])
    setNewConstraint('')
  }

  function addCriterion() {
    const v = newCriterion.trim()
    if (!v) return
    setSuccessCriteria((prev) => [...prev, v])
    setNewCriterion('')
  }

  async function handleSubmit() {
    if (!activeSessionId || !goal.trim() || submitting) return
    setSubmitting(true)
    try {
      const req: CreateRunReq = {
        session_id: activeSessionId,
        goal: goal.trim(),
        mode,
        constraints: constraints.length ? constraints : undefined,
        success_criteria: successCriteria.length ? successCriteria : undefined,
      }
      const idempotencyKey = crypto.randomUUID()
      const res = await api.createRun(req, idempotencyKey)
      await activateRun(res.run_id)
      setGoal('')
      setConstraints([])
      setSuccessCriteria([])
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between py-3">
        <button
          type="button"
          onClick={() => setShowAdvanced((v) => !v)}
          className="flex items-center gap-1.5 text-sm font-semibold"
        >
          {showAdvanced ? (
            <ChevronDown className="h-4 w-4" />
          ) : (
            <ChevronRight className="h-4 w-4" />
          )}
          New Run
        </button>
        <Rocket className="h-4 w-4 text-primary" />
      </CardHeader>

      <CardContent className="flex flex-col gap-3 pt-0">
        <Textarea
          value={goal}
          onChange={(e) => setGoal(e.target.value)}
          placeholder="Describe the goal…"
          rows={3}
          required
        />

        {showAdvanced && (
          <div className="flex flex-col gap-3">
            <div className="flex items-center gap-2">
              <span className="text-xs text-muted-foreground/70">Mode</span>
              <div className="flex gap-1">
                {(['react', 'plan_execute'] as Mode[]).map((m) => (
                  <Button
                    key={m}
                    type="button"
                    size="sm"
                    variant={mode === m ? 'default' : 'outline'}
                    onClick={() => setMode(m)}
                    className={cn(mode === m ? 'bg-primary text-primary-foreground hover:bg-primary/90' : '')}
                  >
                    {m}
                  </Button>
                ))}
              </div>
            </div>

            <div className="flex flex-col gap-1.5">
              <span className="text-xs text-muted-foreground/70">Constraints</span>
              {constraints.map((c, i) => (
                <div key={i} className="flex items-center gap-2">
                  <span className="flex-1 truncate text-sm text-foreground/90">{c}</span>
                  <Button
                    type="button"
                    size="icon"
                    variant="ghost"
                    className="h-6 w-6 text-muted-foreground/70"
                    onClick={() =>
                      setConstraints((prev) => prev.filter((_, idx) => idx !== i))
                    }
                  >
                    <Minus className="h-3 w-3" />
                  </Button>
                </div>
              ))}
              <div className="flex gap-2">
                <Input
                  value={newConstraint}
                  onChange={(e) => setNewConstraint(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') addConstraint()
                  }}
                  placeholder="Add a constraint…"
                  className="h-8 text-xs"
                />
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  onClick={addConstraint}
                  disabled={!newConstraint.trim()}
                >
                  <Plus className="h-3.5 w-3.5" />
                </Button>
              </div>
            </div>

            <div className="flex flex-col gap-1.5">
              <span className="text-xs text-muted-foreground/70">Success criteria</span>
              {successCriteria.map((c, i) => (
                <div key={i} className="flex items-center gap-2">
                  <span className="flex-1 truncate text-sm text-foreground/90">{c}</span>
                  <Button
                    type="button"
                    size="icon"
                    variant="ghost"
                    className="h-6 w-6 text-muted-foreground/70"
                    onClick={() =>
                      setSuccessCriteria((prev) => prev.filter((_, idx) => idx !== i))
                    }
                  >
                    <Minus className="h-3 w-3" />
                  </Button>
                </div>
              ))}
              <div className="flex gap-2">
                <Input
                  value={newCriterion}
                  onChange={(e) => setNewCriterion(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') addCriterion()
                  }}
                  placeholder="Add a success criterion…"
                  className="h-8 text-xs"
                />
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  onClick={addCriterion}
                  disabled={!newCriterion.trim()}
                >
                  <Plus className="h-3.5 w-3.5" />
                </Button>
              </div>
            </div>
          </div>
        )}

        <Separator />
        <div className="flex items-center justify-between">
          <span className="text-xs text-muted-foreground/70">
            {activeSessionId ? 'Creates a run in the active session' : 'No active session'}
          </span>
          <Button
            onClick={() => void handleSubmit()}
            disabled={!goal.trim() || submitting || !activeSessionId}
          >
            {submitting ? 'Submitting…' : 'Start Run'}
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}
