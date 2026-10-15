import { describe, it, expect } from 'vitest'
import { eventSummary } from '@/lib/status'
import type { AgentEvent, EventType } from '@/lib/types'

function ev(type: EventType, payload: Record<string, unknown> = {}): AgentEvent {
  return { seq: 1, run_id: 'r1', event_type: type, payload_json: JSON.stringify(payload), sensitivity: 'normal', created_at: 0 }
}

describe('eventSummary', () => {
  it.each([
    ['tool.requested', 'requesting shell'],
    ['tool.started', 'tool shell started'],
    ['tool.completed', 'tool shell completed'],
    ['tool.failed', 'tool shell failed'],
    ['tool.authorized', 'tool shell authorized'],
    ['tool.denied', 'tool shell denied'],
  ] as const)('%s reads p.tool', (type, expected) => {
    expect(eventSummary(ev(type, { tool: 'shell' }))).toBe(expected)
  })

  it('falls back to static text when p.tool is absent', () => {
    expect(eventSummary(ev('tool.started'))).toBe('tool started')
  })

  it.each([
    ['model.requested', 'model call started'],
    ['model.completed', 'model call completed'],
    ['model.failed', 'model call failed'],
  ] as const)('%s has no call number', (type, expected) => {
    expect(eventSummary(ev(type, { text: 'ok' }))).toBe(expected)
  })

  it('labels unknown event types instead of leaking the raw name', () => {
    expect(eventSummary(ev('weird.new.event' as EventType))).toBe('Unknown event weird.new.event')
  })
})
