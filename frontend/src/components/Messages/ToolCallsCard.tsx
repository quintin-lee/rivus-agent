import { useState } from 'react'
import { ChevronDown, ChevronRight, Wrench } from 'lucide-react'

type ToolsMessage = {
  kind: 'system-tools'
  id: string
  runId: string
  calls: { tool: string; args?: string; output?: string; seq: number }[]
}

export function ToolCallsCard({ message }: { message: ToolsMessage }) {
  const [open, setOpen] = useState(false)
  if (message.calls.length === 0) return null
  return (
    <div className="rounded-lg border border-border bg-card/60 px-3 py-2">
      <button
        onClick={() => setOpen(!open)}
        className="flex w-full items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground"
      >
        {open ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
        <Wrench className="h-3.5 w-3.5" />
        <span>工具调用 ×{message.calls.length}</span>
      </button>
      {open && (
        <div className="mt-2 flex flex-col gap-2 border-t border-border pt-2">
          {message.calls.map((c) => (
            <div key={c.seq} className="rounded border border-border bg-muted/30 p-2">
              <div className="text-[11px] font-medium text-teal-400">{c.tool}</div>
              {c.args && (
                <pre className="mt-1 max-h-24 overflow-y-auto whitespace-pre-wrap break-all text-[10px] text-muted-foreground">
                  {c.args}
                </pre>
              )}
              {c.output && (
                <pre className="mt-1 max-h-32 overflow-y-auto whitespace-pre-wrap break-all text-[10px] text-foreground/80">
                  {c.output}
                </pre>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
