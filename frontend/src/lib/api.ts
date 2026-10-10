import { API_BASE, OWNER_ID } from '@/App.config'
import type {
  AgentEvent,
  CreateRunReq,
  ModelSettings,
  Run,
  Session,
  Step,
} from './types'

export class ApiError extends Error {
  code: string
  retryable: boolean

  constructor(code: string, message: string, retryable: boolean) {
    super(message)
    this.name = 'ApiError'
    this.code = code
    this.retryable = retryable
  }
}

class ApiClient {
  private async request<T>(
    method: string,
    path: string,
    body?: unknown,
    headers?: Record<string, string>,
  ): Promise<T> {
    const opts: RequestInit = {
      method,
      headers: {
        'Content-Type': 'application/json',
        'X-Owner-ID': OWNER_ID,
        ...headers,
      },
    }
    if (body !== undefined) {
      opts.body = JSON.stringify(body)
    }

    const res = await fetch(`${API_BASE}${path}`, opts)
    if (!res.ok) {
      let code = `http_${res.status}`
      let message = res.statusText
      let retryable = res.status >= 500
      try {
        const errJson = await res.json()
        if (errJson.code) code = errJson.code
        if (errJson.message) message = errJson.message
        if (typeof errJson.retryable === 'boolean') retryable = errJson.retryable
      } catch {
        // fall through with default error values
      }
      throw new ApiError(code, message, retryable)
    }

    if (res.status === 204) {
      return undefined as T
    }
    return (await res.json()) as T
  }

  async createSession(title: string): Promise<{ session_id: string }> {
    return this.request<{ session_id: string }>('POST', '/api/v1/sessions', {
      title,
    })
  }

  async getSession(id: string): Promise<Session> {
    return this.request<Session>('GET', `/api/v1/sessions/${encodeURIComponent(id)}`)
  }

  async deleteSession(id: string): Promise<void> {
    await this.request('DELETE', `/api/v1/sessions/${encodeURIComponent(id)}`)
  }

  async listSessions(): Promise<Session[]> {
    try {
      const data = await this.request<{ sessions: Session[] }>('GET', '/api/v1/sessions')
      return data.sessions ?? []
    } catch (err) {
      if (err instanceof ApiError && err.code === 'not_found') return []
      throw err
    }
  }

  async listRuns(sessionId: string, limit = 50): Promise<Run[]> {
    try {
      const data = await this.request<{ runs: Run[] }>(
        'GET',
        `/api/v1/runs?session_id=${encodeURIComponent(sessionId)}&limit=${limit}`,
      )
      return data.runs ?? []
    } catch (err) {
      if (err instanceof ApiError && err.code === 'not_found') return []
      throw err
    }
  }

  async createRun(
    req: CreateRunReq,
    idempotencyKey?: string,
  ): Promise<{ run_id: string; status: string; duplicated: boolean }> {
    const headers: Record<string, string> = {}
    if (idempotencyKey) {
      headers['Idempotency-Key'] = idempotencyKey
    }
    return this.request('POST', '/api/v1/runs', req, headers)
  }

  async getRun(id: string): Promise<{ run: Run; steps: Step[] }> {    const data = await this.request<{ run: Run; steps: Step[] }>(
      'GET',
      `/api/v1/runs/${encodeURIComponent(id)}`,
    )
    return { run: data.run, steps: data.steps ?? [] }
  }

  async getEvents(runId: string, afterSeq: number): Promise<AgentEvent[]> {
    const url = `/api/v1/runs/${encodeURIComponent(runId)}/events?after=${afterSeq}`
    const headers: Record<string, string> = {
      'X-Owner-ID': OWNER_ID,
    }
    const res = await fetch(`${API_BASE}${url}`, {
      method: 'GET',
      headers,
    })
    if (!res.ok) {
      let code = `http_${res.status}`
      let message = res.statusText
      let retryable = res.status >= 500
      try {
        const errJson = await res.json()
        if (errJson.code) code = errJson.code
        if (errJson.message) message = errJson.message
        if (typeof errJson.retryable === 'boolean') retryable = errJson.retryable
      } catch {
        // fall through
      }
      throw new ApiError(code, message, retryable)
    }

    const text = await res.text()
    return parseSSE(text, runId)
  }

  async cancelRun(id: string): Promise<void> {
    await this.request('POST', `/api/v1/runs/${encodeURIComponent(id)}/cancel`, {})
  }

  async resumeRun(id: string): Promise<void> {
    await this.request('POST', `/api/v1/runs/${encodeURIComponent(id)}/resume`, {})
  }

  async decideApproval(
    runId: string,
    approvalId: string,
    approve: boolean,
    reason?: string,
  ): Promise<void> {
    await this.request(
      'POST',
      `/api/v1/runs/${encodeURIComponent(runId)}/approvals/${encodeURIComponent(approvalId)}`,
      { approve, reason },
    )
  }

  async getModelSettings(): Promise<ModelSettings> {
    return this.request<ModelSettings>('GET', '/api/v1/settings/model')
  }

  async updateModelSettings(patch: {
    provider?: string
    base_url?: string
    model?: string
    api_key?: string
  }): Promise<void> {
    await this.request('PUT', '/api/v1/settings/model', patch)
  }
}

function parseSSE(text: string, runId: string): AgentEvent[] {
  const blocks = text.split('\n\n')
  const events: AgentEvent[] = []
  for (const block of blocks) {
    if (!block.trim()) continue
    let seq = 0
    let eventType = ''
    let data = ''
    for (const line of block.split('\n')) {
      if (line.startsWith('id: ')) {
        seq = parseInt(line.slice(4), 10) || 0
      } else if (line.startsWith('event: ')) {
        eventType = line.slice(7)
      } else if (line.startsWith('data: ')) {
        data = line.slice(6)
      }
    }
    if (eventType) {
      events.push({
        seq,
        run_id: runId,
        event_type: eventType as AgentEvent['event_type'],
        payload_json: data,
        sensitivity: 'normal',
        created_at: 0,
      })
    }
  }
  return events
}

export const api = new ApiClient()
