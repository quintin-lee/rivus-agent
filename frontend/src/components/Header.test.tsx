import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { Header } from '@/components/Header'
import { api } from '@/lib/api'
import type { Run } from '@/lib/types'

vi.mock('@/lib/api', () => ({ api: { listRuns: vi.fn() } }))

describe('Header run-history entry', () => {
  it('opens the run history dialog from the header button', async () => {
    vi.mocked(api.listRuns).mockResolvedValue([
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
    ])
    render(<Header />)
    fireEvent.click(screen.getByRole('button', { name: /运行历史/ }))
    await waitFor(() => expect(screen.getByText(/历史目标/)).toBeTruthy())
  })
})
