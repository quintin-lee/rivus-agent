import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { ChatInputBar } from '@/components/ChatInputBar'
import { useSessionStore } from '@/store/session-store'
import { useRunStore } from '@/store/run-store'
import { api } from '@/lib/api'
import type { Run } from '@/lib/types'
import type { SubmitRunInput } from '@/store/run-store'

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

function seedStores(submitRunMock?: () => Promise<void>) {
  const submitRun = submitRunMock ?? vi.fn<() => Promise<void>>().mockResolvedValue(undefined)
  useRunStore.setState({
    activeRun: null,
    activeSteps: [],
    events: [],
    pendingGoals: [],
    runHistory: [],
    cancelling: false,
    cancelError: null,
    submitRun: submitRun as unknown as (input: SubmitRunInput) => Promise<void>,
  })
  useSessionStore.setState({ activeSessionId: 's1' })
  return submitRun
}

function runningRun(status: Run['status'] = 'running'): Run {
  return {
    id: 'r1',
    session_id: 's1',
    owner_id: 'o1',
    status,
    mode: 'react',
    goal: 'do the thing',
    budget: {
      max_duration_seconds: 60,
      max_model_calls: 5,
      max_tool_calls: 5,
      max_iterations: 5,
      max_output_bytes: 1024,
    },
    checkpoint_id: '',
    attempt: 1,
    created_at: 1,
    updated_at: 1,
  }
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

describe('ChatInputBar Stop', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('shows Stop instead of Send while the active run is in progress', () => {
    seedStores()
    useRunStore.setState({ activeRun: runningRun() })
    render(<ChatInputBar />)
    expect(screen.getByRole('button', { name: /stop/i })).toBeTruthy()
    expect(screen.queryByRole('button', { name: /send/i })).toBeNull()
  })

  it('calls api.cancelRun with the active run id when Stop is clicked', async () => {
    seedStores()
    useRunStore.setState({ activeRun: runningRun() })
    render(<ChatInputBar />)
    fireEvent.click(screen.getByRole('button', { name: /stop/i }))
    await new Promise((r) => setTimeout(r, 0))
    expect(api.cancelRun).toHaveBeenCalledWith('r1')
  })

  it('disables the Stop button while cancelling', () => {
    seedStores()
    useRunStore.setState({ activeRun: runningRun(), cancelling: true })
    render(<ChatInputBar />)
    const stopBtn = screen.getByRole('button', { name: /stopping/i }) as HTMLButtonElement
    expect(stopBtn.disabled).toBe(true)
  })

  it('renders cancelError inline', () => {
    seedStores()
    useRunStore.setState({ activeRun: runningRun(), cancelError: 'Stop failed' })
    render(<ChatInputBar />)
    expect(screen.getByText(/stop failed/i)).toBeTruthy()
  })

  it('shows Send again when the active run is terminal', () => {
    seedStores()
    useRunStore.setState({ activeRun: runningRun('succeeded') })
    render(<ChatInputBar />)
    expect(screen.getByRole('button', { name: /send/i })).toBeTruthy()
    expect(screen.queryByRole('button', { name: /stop/i })).toBeNull()
  })
})
