import type { AgentEvent, RunStatus } from './types'

export type PollStatus = 'polling' | 'error' | 'stopped'

export interface PollerOpts {
  getEvents: (runId: string, afterSeq: number) => Promise<AgentEvent[]>
  getRunStatus: (runId: string) => Promise<RunStatus>
  runId: string
  intervalMs?: number
  getAfterSeq: () => number
  onEvent: (e: AgentEvent) => void
  onStatus: (s: PollStatus) => void
}

const TERMINAL: Set<RunStatus> = new Set([
  'succeeded',
  'failed',
  'cancelled',
  'timed_out',
])

export function startEventPoller(opts: PollerOpts): () => void {
  const interval = opts.intervalMs ?? 2000
  let afterSeq = opts.getAfterSeq()
  let stopped = false
  let timer: ReturnType<typeof setTimeout> | null = null
  let backoffMs = 0

  async function tick() {
    if (stopped) return
    try {
      const events = await opts.getEvents(opts.runId, afterSeq)
      backoffMs = 0
      opts.onStatus('polling')
      for (const e of events) {
        opts.onEvent(e)
        if (e.seq > afterSeq) afterSeq = e.seq
      }
      const status = await opts.getRunStatus(opts.runId)
      if (TERMINAL.has(status)) {
        stop()
        opts.onStatus('stopped')
        return
      }
    } catch {
      if (!stopped) {
        opts.onStatus('error')
        backoffMs = Math.min(backoffMs + 1000, 30_000)
        timer = setTimeout(tick, backoffMs)
        return
      }
    }
    if (!stopped) {
      timer = setTimeout(tick, interval)
    }
  }

  function stop() {
    stopped = true
    if (timer) clearTimeout(timer)
  }

  timer = setTimeout(tick, 0)
  return stop
}
