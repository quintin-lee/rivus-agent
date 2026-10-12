import { useState } from 'react'
import { useSessionStore } from '@/store/session-store'
import { useRunStore } from '@/store/run-store'
import { assembleBudget, BUDGET_FIELDS, type BudgetKey } from '@/lib/budget'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function NewRunDialog({ open, onOpenChange }: Props) {
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const submitRun = useRunStore((s) => s.submitRun)
  const [goal, setGoal] = useState('')
  const [mode, setMode] = useState<'react' | 'plan_execute'>('react')
  const [constraints, setConstraints] = useState<string[]>([])
  const [successCriteria, setSuccessCriteria] = useState<string[]>([])
  const [budgetInput, setBudgetInput] = useState<Partial<Record<BudgetKey, string>>>({})

  const canSubmit = goal.trim().length > 0

  const submit = () => {
    if (!canSubmit || !activeSessionId) return
    const cleanConstraints = constraints.map((c) => c.trim()).filter(Boolean)
    const cleanSuccess = successCriteria.map((c) => c.trim()).filter(Boolean)
    void submitRun({
      sessionId: activeSessionId,
      goal: goal.trim(),
      mode,
      constraints: cleanConstraints.length > 0 ? cleanConstraints : undefined,
      successCriteria: cleanSuccess.length > 0 ? cleanSuccess : undefined,
      budget: assembleBudget(budgetInput),
    })
    setGoal('')
    setConstraints([])
    setSuccessCriteria([])
    setBudgetInput({})
    onOpenChange(false)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-[560px]">
        <DialogHeader>
          <DialogTitle>New Run</DialogTitle>
          <DialogDescription>
            Advanced options for the next run in this session.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          <div className="space-y-1.5">
            <label className="text-xs font-medium">Goal</label>
            <textarea
              className="w-full rounded-lg border border-border bg-card p-2.5 text-sm focus:outline-none focus:ring-2 focus:ring-ring/40"
              rows={3}
              placeholder="Describe the goal…"
              value={goal}
              onChange={(e) => setGoal(e.target.value)}
            />
          </div>

          <div className="space-y-1.5">
            <label className="text-xs font-medium">Mode</label>
            <div className="flex gap-2">
              {(['react', 'plan_execute'] as const).map((m) => (
                <button
                  key={m}
                  type="button"
                  className={
                    'rounded-md border px-3 py-1.5 text-xs ' +
                    (mode === m
                      ? 'border-primary bg-primary/10 font-medium text-primary'
                      : 'border-border text-muted-foreground hover:bg-accent')
                  }
                  onClick={() => setMode(m)}
                >
                  {m === 'react' ? 'ReAct' : 'Plan & Execute'}
                </button>
              ))}
            </div>
          </div>

          <ConstraintEditor
            label="Constraints"
            items={constraints}
            onChange={setConstraints}
            placeholder="Add a constraint (e.g. do not delete files)"
          />
          <ConstraintEditor
            label="Success criteria"
            items={successCriteria}
            onChange={setSuccessCriteria}
            placeholder="Add a success criterion"
          />

          <div className="space-y-1.5">
            <label className="text-xs font-medium">Budget (optional)</label>
            <div className="grid grid-cols-2 gap-2">
              {BUDGET_FIELDS.map((f) => (
                <div key={f.key} className="space-y-0.5">
                  <label className="text-[11px] text-muted-foreground">
                    {f.label} <span className="opacity-60">{f.hint}</span>
                  </label>
                  <input
                    className="w-full rounded-md border border-border bg-card px-2 py-1.5 text-xs"
                    inputMode="numeric"
                    placeholder="—"
                    value={budgetInput[f.key] ?? ''}
                    onChange={(e) =>
                      setBudgetInput((prev) => ({ ...prev, [f.key]: e.target.value }))
                    }
                  />
                </div>
              ))}
            </div>
          </div>
        </div>

        <DialogFooter>
          <button
            type="button"
            className="rounded-md border border-border px-3 py-1.5 text-xs hover:bg-accent"
            onClick={() => onOpenChange(false)}
          >
            Cancel
          </button>
          <button
            type="button"
            className="rounded-md bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
            disabled={!canSubmit}
            onClick={submit}
          >
            Create run
          </button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function ConstraintEditor({
  label,
  items,
  onChange,
  placeholder,
}: {
  label: string
  items: string[]
  onChange: (updater: (prev: string[]) => string[]) => void
  placeholder: string
}) {
  const [draft, setDraft] = useState('')
  return (
    <div className="space-y-1.5">
      <label className="text-xs font-medium">{label}</label>
      {items.length > 0 && (
        <ul className="space-y-1">
          {items.map((item, i) => (
            <li key={i} className="flex items-center gap-2 rounded-md border border-border bg-card px-2 py-1 text-xs">
              <span className="flex-1">{item}</span>
              <button
                type="button"
                aria-label={`remove ${label} ${i + 1}`}
                className="text-muted-foreground hover:text-foreground"
                onClick={() => onChange((prev) => prev.filter((_, j) => j !== i))}
              >
                ✕
              </button>
            </li>
          ))}
        </ul>
      )}
      <div className="flex gap-2">
        <input
          className="flex-1 rounded-md border border-border bg-card px-2 py-1.5 text-xs"
          placeholder={placeholder}
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && draft.trim()) {
              onChange((prev) => [...prev, draft.trim()])
              setDraft('')
            }
          }}
        />
        <button
          type="button"
          className="rounded-md border border-border px-2 py-1.5 text-xs hover:bg-accent disabled:opacity-50"
          disabled={!draft.trim()}
          onClick={() => {
            onChange((prev) => [...prev, draft.trim()])
            setDraft('')
          }}
        >
          Add
        </button>
      </div>
    </div>
  )
}
