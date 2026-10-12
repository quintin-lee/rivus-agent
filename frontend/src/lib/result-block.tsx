import { useMemo } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import rehypeSanitize from 'rehype-sanitize'
import { detectResultView } from '@/lib/result-render'

export function ResultViewBlock({ resultJson }: { resultJson: string }) {
  const view = useMemo(() => {
    try {
      return detectResultView(resultJson)
    } catch {
      return { kind: 'raw', text: resultJson } as const
    }
  }, [resultJson])
  return (
    <details className="rounded-lg border border-border bg-card" open={view.kind === 'markdown'}>
      <summary className="cursor-pointer select-none px-3 py-2 text-xs font-medium text-muted-foreground hover:text-foreground">
        Result{view.kind === 'markdown' ? ' (markdown)' : ''}
      </summary>
      <div className="max-h-[400px] overflow-y-auto border-t border-border p-3">
        {view.kind === 'markdown' ? (
          <div className="space-y-2 text-sm text-foreground/90 [&_a]:text-primary [&_a]:underline [&_ol]:list-decimal [&_ol]:pl-5 [&_pre]:overflow-x-auto [&_pre]:rounded [&_pre]:bg-muted [&_pre]:p-2 [&_pre]:font-mono [&_pre]:text-xs [&_ul]:list-disc [&_ul]:pl-5">
            <ReactMarkdown remarkPlugins={[remarkGfm]} rehypePlugins={[rehypeSanitize]}>
              {view.text}
            </ReactMarkdown>
          </div>
        ) : (
          <pre className="overflow-x-auto font-mono text-xs text-foreground/90">
            {view.kind === 'json' ? view.pretty : view.text}
          </pre>
        )}
        <details className="mt-2">
          <summary className="cursor-pointer select-none text-[11px] text-muted-foreground/70 hover:text-foreground">
            Source
          </summary>
          <pre className="mt-1 overflow-x-auto font-mono text-[11px] text-muted-foreground/70">{resultJson}</pre>
        </details>
      </div>
    </details>
  )
}
