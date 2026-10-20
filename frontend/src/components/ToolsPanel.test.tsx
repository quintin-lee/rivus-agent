import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ToolsPanel } from './ToolsPanel'

const { getTools, saveTools } = vi.hoisted(() => ({
  getTools: vi.fn().mockResolvedValue({
    skills: {
      enabled: true,
      available: [{ name: 'demo', description: '演示技能' }],
    },
    mcp: {
      enabled: true,
      servers: [
        {
          name: 'fs',
          status: 'up',
          command: 'npx',
          tools: [{ name: 'read', description: '读文件' }],
        },
      ],
    },
  }),
  saveTools: vi.fn().mockResolvedValue(undefined),
}))

vi.mock('@/lib/api', () => ({
  api: { getTools, saveTools },
  ApiError: class extends Error {},
}))

describe('ToolsPanel', () => {
  beforeEach(() => {
    getTools.mockClear()
    saveTools.mockClear()
  })

  it('renders the skills-available list and a per-server status badge', async () => {
    render(<ToolsPanel />)
    await waitFor(() => expect(getTools).toHaveBeenCalled())
    expect(await screen.findByText('demo')).toBeInTheDocument()
    expect(screen.getByText('已连接')).toBeInTheDocument()
    expect(screen.getByText('read')).toBeInTheDocument()
  })

  it('saves then re-fetches status badges', async () => {
    render(<ToolsPanel />)
    await screen.findByText('demo')
    const callsBefore = getTools.mock.calls.length
    fireEvent.click(await screen.findByRole('button', { name: '保存工具设置' }))
    await waitFor(() => expect(saveTools).toHaveBeenCalled())
    // re-fetch after save (§4.5 refresh-after-save)
    await waitFor(() => expect(getTools.mock.calls.length).toBeGreaterThan(callsBefore))
    expect(await screen.findByText('已保存')).toBeInTheDocument()
  })
})
