import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { RunForm } from './RunForm'
import { useSessionStore } from '@/store/session-store'
import { useRunStore } from '@/store/run-store'

const { createRun, activateRun } = vi.hoisted(() => ({
  createRun: vi.fn().mockResolvedValue({ run_id: 'r1', status: 'queued', duplicated: false }),
  activateRun: vi.fn().mockResolvedValue(undefined),
}))

vi.mock('@/lib/api', () => ({ api: { createRun }, ApiError: class extends Error {} }))

function seed() {
  useSessionStore.setState({ activeSessionId: 's1' })
  useRunStore.setState({
    activeRunId: null,
    activeRun: null,
    activeSteps: [],
    events: [],
    runHistory: [],
    activateRun,
  })
}

async function submitWithGoal() {
  render(<RunForm />)
  fireEvent.change(screen.getByPlaceholderText('Describe the goal…'), { target: { value: 'do thing' } })
  fireEvent.click(screen.getByRole('button', { name: /new run/i }))
  fireEvent.click(screen.getByRole('button', { name: /start run/i }))
  await waitFor(() => expect(createRun).toHaveBeenCalled())
  return createRun.mock.calls[0][0]
}

describe('RunForm budget', () => {
  beforeEach(() => {
    seed()
    vi.clearAllMocks()
  })

  it('passes filled budget value through', async () => {
    render(<RunForm />)
    fireEvent.change(screen.getByPlaceholderText('Describe the goal…'), { target: { value: 'do thing' } })
    fireEvent.click(screen.getByRole('button', { name: /new run/i }))
    fireEvent.change(screen.getByLabelText(/模型调用数/), { target: { value: '10' } })
    fireEvent.click(screen.getByRole('button', { name: /start run/i }))
    await waitFor(() => expect(createRun).toHaveBeenCalled())
    expect(createRun.mock.calls[0][0].budget).toEqual({ max_model_calls: 10 })
  })

  it('omits budget when all blank', async () => {
    const req = await submitWithGoal()
    expect(req.budget).toBeUndefined()
  })
})
