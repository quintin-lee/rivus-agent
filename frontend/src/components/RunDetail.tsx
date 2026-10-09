import { useState } from 'react'
import { Copy, Check } from 'lucide-react'
import { useRunStore } from '@/store/run-store'
import { runStatusClasses, stepStatusClasses, truncate } from '@/lib/status'
import { Separator } from '@/components/ui/separator'
import { cn } from '@/lib/utils'

export function RunDetail() {
  const activeRun = useRunStore((s) => s.activeRun)
  const activeSteps = useRunStore((s) => s.activeSteps)
  const [copied, setCopied] = useState(false)

  if (!activeRun) return null

  const runId = activeRun.id
  const budget = activeRun.budget

  async function copyId() {
    try {
      await navigator.clipboard.writeText(runId)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1500)
    } catch {
      // ignore
    }
  }

  return (
    <section className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <button
          onClick={() => void copyId()}
          className={cn(
            'flex items-center gap-1.5 rounded border border-zinc-800 bg-zinc-900 px-2 py-1 font-mono text-xs text-zinc-400 hover:border-zinc-700 hover:text-zinc-200',
          )}
          title="Copy run id"
        >
          <span className="truncate max-w-[220px]">{truncate(runId, 24)}</span>
          {copied ? (
            <Check className="h-3 w-3 text-emerald-400" />
          ) : (
            <Copy className="h-3 w-3" />
          )}
        </button>
        <span className={cn('border rounded-full px-2 py-0.5 text-xs font-medium', runStatusClasses[activeRun.status])}>
          {activeRun.status}
        </span>
        <span className="text-xs text-zinc-500">mode: {activeRun.mode}</span>
      </div>

      {activeRun.goal && (
        <p className="text-sm text-zinc-300">{activeRun.goal}</p>
      )}

      {activeRun.error_code && (
        <div className="rounded-md border border-red-500/30 bg-red-500/10 p-3 text-sm">
          <div className="font-medium text-red-400">
            {activeRun.error_code}
            {activeRun.error_summary ? `: ${activeRun.error_summary}` : ''}
          </div>
        </div>
      )}

      {activeSteps.length > 0 && (
        <div className="rounded-lg border border-zinc-800 bg-zinc-900/40">
          <div className="border-b border-zinc-800 px-3 py-2 text-xs font-medium text-zinc-400">
            Steps ({activeSteps.length})
          </div>
          <ol className="flex flex-col">
            {activeSteps.map((step) => (
              <li key={step.id} className="flex items-start gap-3 px-3 py-2">
                <span className="mt-0.5 w-6 shrink-0 text-right font-mono text-xs text-zinc-500">
                  {step.step_index}
                </span>
                <div className="flex min-w-0 flex-1 flex-col gap-1">
                  <div className="flex items-center justify-between gap-2">
                    <span className="truncate text-sm text-zinc-200">{step.description}</span>
                    <span className={cn('shrink-0 rounded-full border px-2 py-0.5 text-[10px] font-medium', stepStatusClasses[step.status])}>
                      {step.status}
                    </span>
                  </div>
                  {step.result_summary && (
                    <span className="truncate text-xs text-zinc-500">{step.result_summary}</span>
                  )}
                </div>
              </li>
            ))}
          </ol>
        </div>
      )}

      {activeRun.result_json && (
        <details className="rounded-lg border border-zinc-800 bg-zinc-900/40">
          <summary className="cursor-pointer select-none px-3 py-2 text-xs font-medium text-zinc-400 hover:text-zinc-200">
            Result
          </summary>
          <pre className="overflow-x-auto border-t border-zinc-800 p-3 font-mono text-xs text-zinc-300">
            {formatJson(activeRun.result_json)}
          </pre>
        </details>
      )}

      <Separator />
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-zinc-500">
        <span>Budget:</span>
        <span>{budget.max_model_calls} model calls</span>
        <span>{budget.max_tool_calls} tool calls</span>
        <span>{budget.max_iterations} iterations</span>
        <span>{budget.max_duration_seconds}s</span>
      </div>
    </section>
  )
}

function formatJson(json: string): string {
  try {
    return JSON.stringify(JSON.parse(json), null, 2)
  } catch {
    return json
  }
}
