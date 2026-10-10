import { describe, it, expect } from 'vitest'
import { assembleBudget, BUDGET_FIELDS } from './budget'

describe('assembleBudget', () => {
  it('returns undefined when all blank', () => {
    expect(assembleBudget({})).toBeUndefined()
  })

  it('passes through filled positive ints only', () => {
    expect(
      assembleBudget({ max_model_calls: '10', max_tool_calls: '', max_iterations: 'abc' }),
    ).toEqual({ max_model_calls: 10 })
  })

  it('drops zero/negative/decimal values (blank=omit semantics)', () => {
    expect(assembleBudget({ max_duration_seconds: '0', max_output_bytes: '-5', max_iterations: '2.5' })).toBeUndefined()
  })

  it('exposes 5 fields', () => {
    expect(BUDGET_FIELDS.map((f) => f.key)).toEqual([
      'max_duration_seconds',
      'max_model_calls',
      'max_tool_calls',
      'max_iterations',
      'max_output_bytes',
    ])
  })
})
