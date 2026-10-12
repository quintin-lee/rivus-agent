import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { ChatInputBar } from '@/components/ChatInputBar'
import { useSessionStore } from '@/store/session-store'
import { useRunStore } from '@/store/run-store'

vi.mock('@/lib/api', () => ({
  api: {
    createRun: vi.fn().mockResolvedValue({ run_id: 'r1', status: 'queued', duplicated: false }),
    decideApproval: vi.fn().mockResolvedValue({}),
    cancelRun: vi.fn().mockResolvedValue({}),
    resumeRun: vi.fn().mockResolvedValue({}),
    getRun: vi.fn().mockResolvedValue({ run: null, steps: [] }),
    listRuns: vi.fn().mockResolvedValue([]),
  },
}))

vi.mock('@/components/NewRunDialog', () => ({
  NewRunDialog: (props: { open: boolean; onOpenChange: (o: boolean) => void }) =>
    props.open ? <div data-testid="new-run-dialog" role="dialog">NewRunDialog</div> : null,
}))

function seedStores(submitRunMock?: ReturnType<typeof vi.fn>) {
  const submitRun = submitRunMock ?? vi.fn().mockResolvedValue(undefined)
  useRunStore.setState({
    activeRun: null,
    activeSteps: [],
    events: [],
    pendingGoals: [],
    runHistory: [],
    submitRun: submitRun as unknown as (input: import('@/store/run-store').SubmitRunInput) => Promise<void>,
  })
  useSessionStore.setState({ activeSessionId: 's1' })
  return submitRun
}

describe('ChatInputBar', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('submits goal via submitRun when Enter+Ctrl is pressed and goal is non-empty', () => {
    const submitRun = seedStores()
    render(<ChatInputBar />)
    const textarea = screen.getByRole('textbox') as HTMLTextAreaElement
    fireEvent.change(textarea, { target: { value: 'hello' } })
    fireEvent.keyDown(textarea, { key: 'Enter', ctrlKey: true })
    expect(submitRun).toHaveBeenCalledWith(
      expect.objectContaining({ goal: 'hello', sessionId: 's1' }),
    )
  })

  it('does not submit when goal is blank', () => {
    const submitRun = seedStores()
    render(<ChatInputBar />)
    const textarea = screen.getByRole('textbox') as HTMLTextAreaElement
    fireEvent.keyDown(textarea, { key: 'Enter', ctrlKey: true })
    expect(submitRun).not.toHaveBeenCalled()
  })

  it('opens the NewRunDialog when the gear button is clicked', () => {
    seedStores()
    render(<ChatInputBar />)
    expect(screen.queryByTestId('new-run-dialog')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: /advanced/i }))
    expect(screen.getByTestId('new-run-dialog')).toBeTruthy()
  })

  it('disables Send when a pending goal is in flight', () => {
    seedStores()
    useRunStore.setState({
      pendingGoals: [{ id: 'p1', goal: 'in-flight', status: 'pending' }],
    })
    render(<ChatInputBar />)
    const sendBtn = screen.getByRole('button', { name: /send/i }) as HTMLButtonElement
    expect(sendBtn.disabled).toBe(true)
  })
})
