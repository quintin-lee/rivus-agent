import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { AssistantBubble } from './AssistantBubble'
import type { Run } from '@/lib/types'

function run(status: Run['status'], resultJson?: string): Run {
  return {
    id: 'r1',
    session_id: 's1',
    owner_id: 'o1',
    status,
    mode: 'react',
    goal: 'do thing',
    budget: {
      max_duration_seconds: 60,
      max_model_calls: 10,
      max_tool_calls: 10,
      max_iterations: 5,
      max_output_bytes: 1024,
    },
    checkpoint_id: '',
    attempt: 1,
    created_at: 1,
    updated_at: 1,
    result_json: resultJson,
  }
}

describe('AssistantBubble', () => {
  it('renders the ResultViewBlock when result_json is present and succeeded', () => {
    render(<AssistantBubble run={run('succeeded', '{"ok":true}')} />)
    expect(screen.getAllByText(/Result/i).length).toBeGreaterThanOrEqual(1)
  })

  it('renders an error summary when the run failed', () => {
    const failingRun = { ...run('failed'), error_code: 'E_TIMEOUT', error_summary: 'timed out' } as Run
    render(<AssistantBubble run={failingRun} />)
    expect(screen.getByText(/timed out/i)).toBeTruthy()
  })

  it('renders a neutral line when error_code is "cancelled" and there is no result', () => {
    const run_ = { ...run('cancelled'), error_code: 'cancelled', error_summary: 'cancelled by user' } as Run
    render(<AssistantBubble run={run_} />)
    expect(screen.getByText(/run cancelled/i)).toBeTruthy()
  })

  it('renders the result plus a partial-result note when cancelled with a result', () => {
    const run_ = { ...run('cancelled', '{"ok":true}'), error_code: 'cancelled' } as Run
    render(<AssistantBubble run={run_} />)
    expect(screen.getByText(/cancelled — partial result/i)).toBeTruthy()
  })

  it('renders nothing meaningful when there is no result and no error', () => {
    const { container } = render(<AssistantBubble run={run('succeeded') as Run} />)
    expect(container.textContent).toBe('')
  })
})
