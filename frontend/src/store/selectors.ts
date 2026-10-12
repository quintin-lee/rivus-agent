import type { AgentEvent } from '@/lib/types'

export function groupEventsByRun(events: AgentEvent[]): Record<string, AgentEvent[]> {
  const out: Record<string, AgentEvent[]> = {}
  for (const e of events) {
    const list = (out[e.run_id] ??= [])
    list.push(e)
  }
  return out
}
