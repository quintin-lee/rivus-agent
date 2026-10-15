import type { Run } from '@/lib/types'
import { ResultViewBlock } from '@/lib/result-block'

export function AssistantBubble({ run }: { run: Run }) {
  if (run.error_code === 'cancelled') {
    if (run.result_json) {
      return (
        <div className="rounded-lg border border-border bg-card p-3">
          <p className="mb-2 text-xs font-medium text-muted-foreground">
            Cancelled — partial result
          </p>
          <ResultViewBlock resultJson={run.result_json} />
        </div>
      )
    }
    return (
      <p className="rounded-lg border border-border bg-card/50 p-3 text-sm text-muted-foreground">
        Run cancelled
      </p>
    )
  }
  if (run.error_code) {
    return (
      <div className="rounded-lg border border-rose-500/30 bg-rose-500/10 p-3 text-sm">
        <p className="font-medium text-rose-600 dark:text-rose-400">Run failed</p>
        {run.error_summary && <p className="mt-1 text-rose-700/80 dark:text-rose-300/80">{run.error_summary}</p>}
      </div>
    )
  }
  if (run.result_json) {
    return (
      <div className="rounded-lg border border-border bg-card p-3">
        <p className="mb-2 text-xs font-medium text-muted-foreground">Result</p>
        <ResultViewBlock resultJson={run.result_json} />
      </div>
    )
  }
  return null
}
