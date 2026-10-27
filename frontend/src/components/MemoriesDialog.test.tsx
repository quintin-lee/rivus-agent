import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MemoriesDialog } from '@/components/MemoriesDialog'
import { api } from '@/lib/api'
import type { Memory } from '@/lib/types'

vi.mock('@/lib/api', () => ({ api: { listMemories: vi.fn() } }))

function mem(content: string, i = 0): Memory {
  return {
    id: `m${i}`,
    session_id: 's1',
    owner_id: 'o1',
    content,
    source_run_id: `run-${i}abcdef0`,
    created_at: 1700000000000 + i,
  }
}

describe('MemoriesDialog', () => {
  beforeEach(() => vi.mocked(api.listMemories).mockReset())

  it('lists memories newest first with content', async () => {
    vi.mocked(api.listMemories).mockResolvedValue([mem('喜欢 Go', 1), mem('项目用 SQLite', 0)])
    render(<MemoriesDialog sessionId="s1" sessionTitle="t" open onOpenChange={() => {}} />)
    await waitFor(() => expect(screen.getByText(/喜欢 Go/)).toBeTruthy())
    expect(screen.getByText(/项目用 SQLite/)).toBeTruthy()
  })

  it('shows empty state', async () => {
    vi.mocked(api.listMemories).mockResolvedValue([])
    render(<MemoriesDialog sessionId="s1" sessionTitle="t" open onOpenChange={() => {}} />)
    await waitFor(() => expect(screen.getByText(/无记忆/)).toBeTruthy())
  })

  it('filters client-side case-insensitively', async () => {
    vi.mocked(api.listMemories).mockResolvedValue([mem('Uses SQLite', 0), mem('偏好中文回复', 1)])
    render(<MemoriesDialog sessionId="s1" sessionTitle="t" open onOpenChange={() => {}} />)
    await waitFor(() => expect(screen.getByText(/Uses SQLite/)).toBeTruthy())
    fireEvent.change(screen.getByPlaceholderText(/过滤/), { target: { value: 'SQLITE' } })
    expect(screen.getByText(/Uses SQLite/)).toBeTruthy()
    expect(screen.queryByText(/偏好中文回复/)).toBeNull()
  })

  it('shows error with retry', async () => {
    vi.mocked(api.listMemories).mockRejectedValueOnce(new Error('boom'))
    render(<MemoriesDialog sessionId="s1" sessionTitle="t" open onOpenChange={() => {}} />)
    await waitFor(() => expect(screen.getByText(/boom|加载失败/)).toBeTruthy())
    vi.mocked(api.listMemories).mockResolvedValue([mem('ok', 0)])
    fireEvent.click(screen.getByRole('button', { name: /重试/ }))
    await waitFor(() => expect(screen.getByText(/ok/)).toBeTruthy())
  })
})
