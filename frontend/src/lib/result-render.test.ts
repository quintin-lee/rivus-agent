import { describe, it, expect } from 'vitest'
import { detectResultView } from './result-render'

describe('detectResultView', () => {
  it('picks markdown field from object', () => {
    const r = detectResultView(JSON.stringify({ markdown: '# Hi\n\n- a' }))
    expect(r.kind).toBe('markdown')
    if (r.kind === 'markdown') expect(r.text).toBe('# Hi\n\n- a')
  })

  it('picks text/content/answer field in order', () => {
    expect(detectResultView(JSON.stringify({ text: 'hello' })).kind).toBe('markdown')
    expect(detectResultView(JSON.stringify({ content: 'hello' })).kind).toBe('markdown')
    expect(detectResultView(JSON.stringify({ answer: 'hello' })).kind).toBe('markdown')
  })

  it('detects markdown signals in plain string', () => {
    const r = detectResultView('"## Title\\n\\n[link](http://x)\\n\\n```js\\ncode\\n```"')
    expect(r.kind).toBe('markdown')
  })

  it('falls back to pretty json for plain objects', () => {
    const r = detectResultView(JSON.stringify({ a: 1 }))
    expect(r.kind).toBe('json')
    if (r.kind === 'json') expect(r.pretty).toBe(JSON.stringify({ a: 1 }, null, 2))
  })

  it('falls back to raw pre on invalid json', () => {
    const r = detectResultView('not json {{{')
    expect(r.kind).toBe('raw')
    if (r.kind === 'raw') expect(r.text).toBe('not json {{{')
  })

  it('falls back to raw pre on non-string field values', () => {
    const r = detectResultView(JSON.stringify({ markdown: 42 }))
    expect(r.kind).toBe('json')
  })
})
