import { describe, it, expect, vi, afterEach } from 'vitest'
import { api, ApiError } from '@/lib/api'

function mockFetch(status: number, body: unknown = {}) {
  return vi.fn().mockResolvedValue({
    ok: status < 400,
    status,
    statusText: String(status),
    json: async () => body,
    text: async () => (typeof body === 'string' ? body : JSON.stringify(body)),
  })
}

describe('ApiClient', () => {
  afterEach(() => {
    vi.unstubAllEnvs()
    vi.unstubAllGlobals()
  })

  it('createRun sends Idempotency-Key header and posts body', async () => {
    const f = mockFetch(201, { run_id: 'r1', status: 'queued', duplicated: false })
    vi.stubGlobal('fetch', f)
    const res = await api.createRun({ session_id: 's1', goal: 'g' }, 'idem-1')
    expect(res.run_id).toBe('r1')
    const [url, opts] = f.mock.calls[0] as [string, RequestInit]
    expect(url).toBe('/api/v1/runs')
    expect((opts.headers as Record<string, string>)['Idempotency-Key']).toBe('idem-1')
    expect((opts.headers as Record<string, string>)['X-Owner-ID']).toBe('default')
    expect(JSON.parse(opts.body as string)).toMatchObject({ session_id: 's1', goal: 'g' })
  })

  it('throws ApiError with code on 4xx', async () => {
    const f = mockFetch(404, { code: 'not_found', message: 'run not found', retryable: false })
    vi.stubGlobal('fetch', f)
    let caught: unknown
    try {
      await api.getRun('nope')
    } catch (e) {
      caught = e
    }
    expect(caught).toBeInstanceOf(ApiError)
    const err = caught as ApiError
    expect(err.code).toBe('not_found')
    expect(err.message).toBe('run not found')
    expect(err.retryable).toBe(false)
  })

  it('listSessions returns [] on not_found, rethrows on 5xx', async () => {
    const f = vi.fn()
    vi.stubGlobal('fetch', f)
    f.mockResolvedValueOnce({ ok: true, status: 200, json: async () => ({ sessions: [] }) })
    expect(await api.listSessions()).toEqual([])
    f.mockResolvedValueOnce({
      ok: false,
      status: 500,
      statusText: 'ISE',
      json: async () => ({ code: 'internal', message: 'x', retryable: true }),
    })
    await expect(api.listSessions()).rejects.toMatchObject({ code: 'internal', retryable: true })
  })

  it('getEvents parses SSE frames into AgentEvent[]', async () => {
    const sse =
      'id: 5\nevent: model.requested\ndata: {"call_count":1}\n\nid: 6\nevent: tool.completed\ndata: {"tool_name":"x"}\n\n'
    const f = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      statusText: 'OK',
      text: async () => sse,
      json: async () => ({}),
    })
    vi.stubGlobal('fetch', f)
    const evs = await api.getEvents('r1', 0)
    expect(evs).toHaveLength(2)
    expect(evs[0]).toMatchObject({
      seq: 5,
      event_type: 'model.requested',
      payload_json: '{"call_count":1}',
    })
    expect(evs[1].event_type).toBe('tool.completed')
  })
})
