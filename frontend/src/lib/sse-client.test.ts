import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { startEventStream, type StreamOpts } from './sse-client'
import type { RunStatus } from './types'

function sseResponse(chunks: string[]): Response {
  const stream = new ReadableStream<Uint8Array>({
    start(c) {
      for (const ch of chunks) c.enqueue(new TextEncoder().encode(ch))
      c.close()
    },
  })
  return new Response(stream, { headers: { 'Content-Type': 'text/event-stream' } })
}

/** 模拟“收到若干帧后连接中断”：先吐 chunk，之后 read 抛错 */
function breakAfter(chunks: string[]): Response {
  const enc = new TextEncoder()
  let i = 0
  const reader = {
    read: async (): Promise<ReadableStreamReadResult<Uint8Array>> => {
      if (i < chunks.length) return { done: false, value: enc.encode(chunks[i++]) }
      throw new Error('boom')
    },
    cancel: async () => {},
  }
  return { ok: true, status: 200, body: { getReader: () => reader } } as unknown as Response
}

function frame(seq: number, type: string, data = '{}'): string {
  return `id: ${seq}\nevent: ${type}\ndata: ${data}\n\n`
}

function baseOpts(over: Partial<StreamOpts> = {}) {
  const onEvent = vi.fn()
  const onStatus = vi.fn()
  const onFallback = vi.fn()
  const opts: StreamOpts = {
    runId: 'run_1',
    getAfterSeq: () => 0,
    getRunStatus: async () => 'succeeded' as RunStatus,
    onEvent,
    onStatus,
    onFallback,
    ...over,
  }
  return { opts, onEvent, onStatus, onFallback }
}

async function flush(n = 30) {
  for (let i = 0; i < n; i++) await vi.advanceTimersByTimeAsync(10)
}

let fetchMock: ReturnType<typeof vi.fn>

beforeEach(() => {
  vi.useFakeTimers()
  fetchMock = vi.fn()
  vi.stubGlobal('fetch', fetchMock)
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('startEventStream', () => {
  it('delivers run.started frame and reports streaming', async () => {
    fetchMock.mockResolvedValue(sseResponse([frame(1, 'run.started')]))
    const { opts, onEvent, onStatus } = baseOpts()
    const stop = startEventStream(opts)
    await flush()
    expect(onEvent).toHaveBeenCalledWith(expect.objectContaining({ seq: 1, event_type: 'run.started' }))
    expect(onStatus).toHaveBeenCalledWith('streaming')
    stop()
  })

  it('ignores : ping frames', async () => {
    fetchMock.mockResolvedValue(sseResponse([': ping\n\n', frame(2, 'step.started')]))
    const { opts, onEvent } = baseOpts()
    const stop = startEventStream(opts)
    await flush()
    expect(onEvent).toHaveBeenCalledTimes(1)
    expect(onEvent).toHaveBeenCalledWith(expect.objectContaining({ seq: 2 }))
    stop()
  })

  it('stops without reconnect on terminal event', async () => {
    fetchMock.mockResolvedValue(sseResponse([frame(3, 'run.finished')]))
    const { opts, onStatus, onFallback } = baseOpts()
    const stop = startEventStream(opts)
    await flush()
    await vi.advanceTimersByTimeAsync(60_000)
    expect(onStatus).toHaveBeenCalledWith('stopped')
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(onFallback).not.toHaveBeenCalled()
    stop()
  })

  it('reconnects with backoff and resumes from last seq', async () => {
    let after = 0
    const onEvent = vi.fn((e: { seq: number }) => {
      after = e.seq
    })
    fetchMock
      .mockResolvedValueOnce(breakAfter([frame(7, 'run.started')]))
      .mockResolvedValue(sseResponse([frame(8, 'step.started')]))
    const { opts } = baseOpts({ getAfterSeq: () => after, onEvent })
    const stop = startEventStream(opts)
    await flush()
    expect(fetchMock).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1100)
    await flush()
    expect(fetchMock).toHaveBeenCalledTimes(2)
    const secondUrl = fetchMock.mock.calls[1][0] as string
    expect(secondUrl).toContain('after=7')
    stop()
  })

  it('falls back after exceeding maxRetries', async () => {
    fetchMock.mockRejectedValue(new Error('down'))
    const { opts, onStatus, onFallback } = baseOpts({ maxRetries: 2 })
    const stop = startEventStream(opts)
    await flush()
    await vi.advanceTimersByTimeAsync(1000)
    await flush()
    await vi.advanceTimersByTimeAsync(2000)
    await flush()
    expect(fetchMock).toHaveBeenCalledTimes(3)
    expect(onFallback).toHaveBeenCalledTimes(1)
    expect(onStatus).toHaveBeenCalledWith('stopped')
    stop()
  })
})
