import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { NewRunDialog } from './NewRunDialog'
import { useRunStore } from '@/store/run-store'
import { useSessionStore } from '@/store/session-store'
import type { SubmitRunInput } from '@/store/run-store'

vi.mock('@/lib/api', () => ({ api: {} }))

function setSubmitRunMock(fn: (input: SubmitRunInput) => Promise<void>) {
  useRunStore.setState({
    submitRun: fn as unknown as (input: SubmitRunInput) => Promise<void>,
  } as unknown as Parameters<typeof useRunStore.setState>[0])
}

function openDialog() {
  useSessionStore.setState({ activeSessionId: 's1' } as unknown as Parameters<typeof useSessionStore.setState>[0])
  render(<NewRunDialog open onOpenChange={vi.fn()} />)
}

describe('NewRunDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('passes goal, constraints, successCriteria, and budget to submitRun on submit', async () => {
    const spy = vi.fn().mockResolvedValue(undefined)
    setSubmitRunMock(spy)

    openDialog()

    // Fill goal (textarea placeholder: "Describe the goal…")
    const textarea = screen.getByPlaceholderText(/Describe the goal/i)
    fireEvent.change(textarea, { target: { value: 'ship it' } })

    // Add a constraint
    const constraintInput = screen.getByPlaceholderText(/Add a constraint/i)
    fireEvent.change(constraintInput, { target: { value: 'no rm -rf' } })
    // There are two "Add" buttons (constraints + success criteria) — click the first one
    const addButtons = screen.getAllByRole('button', { name: 'Add' })
    fireEvent.click(addButtons[0])

    // Add a success criterion
    const successInput = screen.getByPlaceholderText(/Add a success criterion/i)
    fireEvent.change(successInput, { target: { value: 'tests pass' } })
    fireEvent.click(screen.getAllByRole('button', { name: 'Add' })[1])

    // Fill the first budget input (budget inputs have placeholder "—")
    const budgetInputs = screen.getAllByPlaceholderText('—')
    fireEvent.change(budgetInputs[0], { target: { value: '300' } })

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /create run/i }))
    })

    expect(spy).toHaveBeenCalledTimes(1)
    const arg = spy.mock.calls[0][0]
    expect(arg.goal).toBe('ship it')
    expect(arg.constraints).toEqual(['no rm -rf'])
    expect(arg.successCriteria).toEqual(['tests pass'])
    expect(arg.budget).toEqual({ max_duration_seconds: 300 })
  })

  it('omits budget when all budget fields are blank', async () => {
    const spy = vi.fn().mockResolvedValue(undefined)
    setSubmitRunMock(spy)

    openDialog()

    const textarea = screen.getByPlaceholderText(/Describe the goal/i)
    fireEvent.change(textarea, { target: { value: 'goal' } })

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /create run/i }))
    })

    const arg = spy.mock.calls[0][0]
    expect(arg.budget).toBeUndefined()
  })
})
