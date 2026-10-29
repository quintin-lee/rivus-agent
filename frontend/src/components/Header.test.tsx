import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { Header } from '@/components/Header'
import { api } from '@/lib/api'
import { useUiStore } from '@/store/ui-store'
import type { Run } from '@/lib/types'

vi.mock('@/lib/api', () => ({ api: { listRunsPage: vi.fn() } }))

describe('Header theme toggle', () => {
  beforeEach(() => {
    useUiStore.setState({ theme: 'light' })
  })
  afterEach(() => {
    localStorage.removeItem('rivus-theme')
    document.documentElement.classList.remove('dark')
  })

  it('cycles theme on button click', () => {
    render(<Header />)
    fireEvent.click(screen.getByRole('button', { name: '切换主题' }))
    expect(useUiStore.getState().theme).toBe('dark')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    expect(screen.getByTitle('主题：深色')).toBeTruthy()
  })
})

describe('Header run-history entry', () => {
  it('opens the run history dialog from the header button', async () => {
    vi.mocked(api.listRunsPage).mockResolvedValue({ items: [
      {
        id: 'r1',
        session_id: 's1',
        owner_id: 'o1',
        status: 'succeeded',
        mode: 'react',
        goal: '历史目标',
        budget: {},
        attempt: 1,
        created_at: 1,
        updated_at: 1,
      } as Run,
    ], nextCursor: '' })
    render(<Header />)
    fireEvent.click(screen.getByRole('button', { name: /运行历史/ }))
    await waitFor(() => expect(screen.getByText(/历史目标/)).toBeTruthy())
  })
})

describe('Header language toggle', () => {
  beforeEach(() => {
    localStorage.clear()
    useUiStore.setState({ lang: 'zh' })
  })

  it('toggles zh<->en on click', () => {
    render(<Header />)
    // zh 界面上按钮显示目标语言 EN
    const btn = screen.getByRole('button', { name: '切换语言' })
    expect(btn.textContent).toBe('EN')
    fireEvent.click(btn)
    expect(useUiStore.getState().lang).toBe('en')
    // en 界面上按钮显示目标语言 中
    expect(screen.getByRole('button', { name: '切换语言' }).textContent).toBe('中')
    fireEvent.click(screen.getByRole('button', { name: '切换语言' }))
    expect(useUiStore.getState().lang).toBe('zh')
  })
})
