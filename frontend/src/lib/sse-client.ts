import { API_BASE, OWNER_ID } from '@/App.config'
import type { AgentEvent, RunStatus } from './types'

export type StreamStatus = 'streaming' | 'error' | 'stopped'

export interface StreamOpts {
  runId: string
  getAfterSeq: () => number
  getRunStatus: (runId: string) => Promise<RunStatus>
  onEvent: (e: AgentEvent) => void
  onStatus: (s: StreamStatus) => void
  /** 连续失败 maxRetries 次后调用，调用方切 poller 降级 */
  onFallback: () => void
  maxRetries?: number
}

const TERMINAL_EVENTS = new Set(['run.finished', 'run.failed', 'run.cancelled'])
const TERMINAL_STATUS = new Set<RunStatus>(['succeeded', 'failed', 'cancelled', 'timed_out'])

function parseBlock(block: string, runId: string): AgentEvent | null {
  let seq = 0
  let eventType = ''
  const dataLines: string[] = []
  for (const line of block.split('\n')) {
    if (line.startsWith('id: ')) seq = parseInt(line.slice(4), 10) || 0
    else if (line.startsWith('event: ')) eventType = line.slice(7)
    else if (line.startsWith('data: ')) dataLines.push(line.slice(6))
    else if (line.startsWith(':')) continue // ping/注释帧
  }
  if (!eventType) return null
  return { seq, run_id: runId, event_type: eventType as AgentEvent['event_type'], payload_json: dataLines.join('\n'), sensitivity: 'normal', created_at: 0 }
}

export function startEventStream(opts: StreamOpts): () => void {
  const maxRetries = opts.maxRetries ?? 3
  let stopped = false
  let failures = 0
  let backoffMs = 1000
  let timer: ReturnType<typeof setTimeout> | null = null
  let reader: ReadableStreamDefaultReader<Uint8Array> | null = null
  const decoder = new TextDecoder()

  async function connect() {
    if (stopped) return
    const after = opts.getAfterSeq()
    let res: Response
    try {
      res = await fetch(`${API_BASE}/api/v1/runs/${encodeURIComponent(opts.runId)}/events?after=${after}`, {
        method: 'GET',
        headers: { 'X-Owner-ID': OWNER_ID, Accept: 'text/event-stream' },
      })
      if (!res.ok || !res.body) throw new Error(`http_${res.status}`)
    } catch {
      return scheduleReconnect()
    }
    failures = 0
    backoffMs = 1000
    opts.onStatus('streaming')
    reader = res.body.getReader()
    let buf = ''
    try {
      for (;;) {
        const { done, value } = await reader.read()
        if (done) break
        buf += decoder.decode(value, { stream: true })
        const blocks = buf.split('\n\n')
        buf = blocks.pop() ?? ''
        for (const block of blocks) {
          if (!block.trim() || block.trimStart().startsWith(':')) continue
          const e = parseBlock(block, opts.runId)
          if (!e) continue
          opts.onEvent(e)
          if (TERMINAL_EVENTS.has(e.event_type)) return finish()
        }
      }
    } catch {
      return scheduleReconnect()
    }
    // 服务端正常关闭（终态后关闭）：确认终态则停，否则重连续播。
    try {
      const status = await opts.getRunStatus(opts.runId)
      if (TERMINAL_STATUS.has(status)) return finish()
    } catch { /* 查不到就重连 */ }
    return scheduleReconnect()
  }

  function scheduleReconnect() {
    if (stopped) return
    failures += 1
    opts.onStatus('error')
    if (failures > maxRetries) {
      finish()
      opts.onFallback()
      return
    }
    timer = setTimeout(connect, backoffMs)
    backoffMs = Math.min(backoffMs * 2, 30_000)
  }

  function finish() {
    stopped = true
    if (timer) clearTimeout(timer)
    void reader?.cancel().catch(() => undefined)
    opts.onStatus('stopped')
  }

  void connect()
  return finish
}
