import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { ToolCallsCard } from '@/components/Messages/ToolCallsCard'
import { useUiStore } from '@/store/ui-store'

const calls = [
  { tool: 'file_search', args: '{"query":"x"}', output: 'line1\nline2', seq: 1 },
  { tool: 'http_fetch', args: '{"url":"http://a"}', output: '{"status":200}', seq: 2 },
]

describe('ToolCallsCard', () => {
  it('renders collapsed summary and expands entries', () => {
    render(<ToolCallsCard message={{ kind: 'system-tools', id: 'x', runId: 'r1', calls }} />)
    expect(screen.getByText(/工具调用 ×2/)).toBeTruthy()
    fireEvent.click(screen.getByRole('button'))
    expect(screen.getByText(/file_search/)).toBeTruthy()
    expect(screen.getByText(/http_fetch/)).toBeTruthy()
  })


  it('copies tool output to clipboard', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.assign(navigator, { clipboard: { writeText } })
    render(<ToolCallsCard message={{ kind: 'system-tools', id: 'x', runId: 'r1', calls }} />)
    fireEvent.click(screen.getByRole('button'))
    fireEvent.click(screen.getAllByLabelText('复制')[0])
    await vi.waitFor(() => expect(writeText).toHaveBeenCalledWith('line1\nline2'))
  })

  it('returns null for empty calls', () => {
    const { container } = render(
      <ToolCallsCard message={{ kind: 'system-tools', id: 'x', runId: 'r1', calls: [] }} />,
    )
    expect(container.firstChild).toBeNull()
  })

  it('renders english title when lang is en', () => {
    useUiStore.setState({ lang: 'en' })
    render(<ToolCallsCard message={{ kind: 'system-tools', id: 'x', runId: 'r1', calls }} />)
    expect(screen.getByText(/Tool calls ×2/)).toBeTruthy()
  })
})
