import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { SessionList } from './SessionList'
import { useSessionStore } from '@/store/session-store'
import { useRunStore } from '@/store/run-store'

vi.mock('@/lib/api', () => ({
  api: {
    listRuns: vi.fn().mockResolvedValue([]),
  },
}))

function seedStores(overrides: Partial<Parameters<typeof useSessionStore.setState>[0]> = {}) {
  useSessionStore.setState({
    sessions: [
      { session_id: 's1', title: 'Session One', created_at: '2025-01-01T00:00:00Z' },
      { session_id: 's2', title: 'Session Two', created_at: '2025-01-02T00:00:00Z' },
    ],
    activeSessionId: 's1',
    createSession: vi.fn().mockResolvedValue(undefined),
    switchSession: vi.fn(),
    loadSessions: vi.fn().mockResolvedValue(undefined),
    deleteSession: vi.fn().mockResolvedValue(undefined),
    renameSession: vi.fn().mockResolvedValue(undefined),
    ...overrides,
  } as unknown as Parameters<typeof useSessionStore.setState>[0])

  useRunStore.setState({
    runHistory: [],
    activeRunId: null,
    activeRun: null,
    activeSteps: [],
    events: [],
    pendingGoals: [],
    activateRun: vi.fn(),
    submitRun: vi.fn().mockResolvedValue(undefined),
  } as unknown as Parameters<typeof useRunStore.setState>[0])
}

describe('SessionList', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders session names with active session highlighted', () => {
    seedStores()
    render(<SessionList />)
    expect(screen.getByText('Session One')).toBeTruthy()
    expect(screen.getByText('Session Two')).toBeTruthy()
    const one = screen.getByText('Session One')
    expect(one.className).toContain('font-medium')
    const two = screen.getByText('Session Two')
    expect(two.className).not.toContain('font-medium')
  })

  it('calls switchSession when a session is clicked', () => {
    const switchSession = vi.fn()
    seedStores({ switchSession })
    render(<SessionList />)
    fireEvent.click(screen.getByText('Session Two'))
    expect(switchSession).toHaveBeenCalledWith('s2')
  })

  it('creates a session when New button is clicked with a title', async () => {
    const createSession = vi.fn().mockResolvedValue(undefined)
    seedStores({ createSession })
    render(<SessionList />)
    const input = screen.getByPlaceholderText(/新会话标题/i) as HTMLInputElement
    fireEvent.change(input, { target: { value: 'New One' } })
    await act(async () => {
      fireEvent.keyDown(input, { key: 'Enter' })
    })
    expect(createSession).toHaveBeenCalledWith('New One')
  })
})

  it('renames a session inline: pencil opens input, enter commits', async () => {
    const renameSession = vi.fn().mockResolvedValue(undefined)
    seedStores({ renameSession })
    render(<SessionList />)
    fireEvent.click(screen.getAllByTitle('重命名会话')[0])
    const input = screen.getByDisplayValue('Session One')
    fireEvent.change(input, { target: { value: 'New Name' } })
    fireEvent.keyDown(input, { key: 'Enter' })
    await vi.waitFor(() => expect(renameSession).toHaveBeenCalledWith('s1', 'New Name'))
  })

  it('esc cancels inline rename without committing', () => {
    seedStores()
    render(<SessionList />)
    fireEvent.click(screen.getAllByTitle('重命名会话')[0])
    fireEvent.keyDown(screen.getByDisplayValue('Session One'), { key: 'Escape' })
    expect(useSessionStore.getState().renameSession).not.toHaveBeenCalled()
    expect(screen.getByText('Session One')).toBeTruthy()
  })

it('debounces search input and reloads with q', async () => {
  vi.useFakeTimers()
  const loadSessions = vi.fn().mockResolvedValue(undefined)
  seedStores({ loadSessions })
  render(<SessionList />)
  fireEvent.change(screen.getByPlaceholderText('搜索会话…'), { target: { value: 'alpha' } })
  expect(loadSessions).not.toHaveBeenCalled()
  await act(async () => {
    await vi.advanceTimersByTimeAsync(250)
  })
  expect(loadSessions).toHaveBeenCalledWith('alpha')
  vi.useRealTimers()
})

it('reloads without q when search cleared', async () => {
  vi.useFakeTimers()
  const loadSessions = vi.fn().mockResolvedValue(undefined)
  seedStores({ loadSessions })
  render(<SessionList />)
  fireEvent.change(screen.getByPlaceholderText('搜索会话…'), { target: { value: 'alpha' } })
  await act(async () => {
    await vi.advanceTimersByTimeAsync(250)
  })
  fireEvent.change(screen.getByPlaceholderText('搜索会话…'), { target: { value: '' } })
  await act(async () => {
    await vi.advanceTimersByTimeAsync(250)
  })
  expect(loadSessions).toHaveBeenLastCalledWith(undefined)
  vi.useRealTimers()
})

it('shows load-more on full page and fetches next offset', async () => {
  const loadSessions = vi.fn().mockResolvedValue(undefined)
  const many = Array.from({ length: 50 }, (_, i) => ({
    session_id: `s${i}`,
    title: `S${i}`,
    created_at: '2025-01-01T00:00:00Z',
  }))
  seedStores({ sessions: many, loadSessions })
  render(<SessionList />)
  const btn = screen.getByText('加载更多')
  await act(async () => {
    fireEvent.click(btn)
  })
  expect(loadSessions).toHaveBeenCalledWith(undefined, 50)
})

it('hides load-more when page is not full', () => {
  seedStores()
  render(<SessionList />)
  expect(screen.queryByText('加载更多')).toBeNull()
})
