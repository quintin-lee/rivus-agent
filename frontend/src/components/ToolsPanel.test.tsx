import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { ToolsPanel } from '@/components/ToolsPanel'

const api = vi.hoisted(() => ({
  saveTools: vi.fn(async () => ({ ok: true })),
  getTools: vi.fn(async () => ({
    skills: { enabled: true, available: [] },
    mcp: { enabled: true, servers: [] },
  })),
}))
vi.mock('@/lib/api', () => ({ api, ApiError: class ApiError extends Error {} }))
const useUiStoreStore = { lang: 'zh' as 'zh' | 'en' }
vi.mock('@/store/ui-store', () => ({
  useUiStore: Object.assign((sel: any) => sel(useUiStoreStore), {
    getState: () => useUiStoreStore,
    setState: () => {},
  }),
}))

function savePayloads(): any[] {
  return api.saveTools.mock.calls.map((c: any[]) => c[0])
}

beforeEach(() => {
  vi.clearAllMocks()
  api.getTools.mockResolvedValue({ skills: { enabled: false, available: [] }, mcp: { enabled: true, servers: [] } })
  api.saveTools.mockResolvedValue({ ok: true })
})

describe('ToolsPanel MCP OAuth UI', () => {
  it('renders default stdio-only server rows', async () => {
    render(<ToolsPanel />)
    fireEvent.click(screen.getByRole('button', { name: /添加服务器|add a server/i }))
    expect(screen.getByPlaceholderText('server')).toBeTruthy()
    expect(screen.getByPlaceholderText('command')).toBeTruthy()
  })

  it('save with http + oauth omits empty client_secret (留空=保留已存)', async () => {
    render(<ToolsPanel />)
    fireEvent.click(screen.getByRole('button', { name: /添加服务器|add a server/i }))
    const names = screen.getAllByPlaceholderText('server')
    fireEvent.change(names[0], { target: { value: 'authsrv' } })
    // 勾选 HTTP 模式 → url 输入出现
    fireEvent.click(screen.getByRole('checkbox', { name: /http/i }))
    const urlInput = screen.getByPlaceholderText(/http:\/\/|https?:\/\//i) as HTMLInputElement
    fireEvent.change(urlInput, { target: { value: 'http://oauth.local/mcp' } })
    // 勾选 OAuth
    fireEvent.click(screen.getByRole('checkbox', { name: /oauth/i }))
    const fields = screen.getAllByPlaceholderText(/token_url|client_id|secret|scopes/i) as HTMLInputElement[]
    // token_url, client_id, client_secret, scopes（顺序按实现）
    fireEvent.change(fields[0], { target: { value: 'http://oauth.local/token' } })
    fireEvent.change(fields[1], { target: { value: 'cid' } })
    fireEvent.change(fields[3], { target: { value: 'read,write' } })
    // 保存
    fireEvent.click(screen.getByRole('button', { name: /保存工具设置|save tool settings/i }))
    await new Promise((r) => setTimeout(r, 30))
    const payload = savePayloads()[savePayloads().length - 1]!
    expect(payload.mcpServers[0].type).toBe('http')
    expect(payload.mcpServers[0].url).toBe('http://oauth.local/mcp')
    expect(payload.mcpServers[0].oauth).toMatchObject({ token_url: 'http://oauth.local/token', client_id: 'cid', scopes: ['read', 'write'] })
    expect(payload.mcpServers[0].oauth).not.toHaveProperty('client_secret')
  })

  it('oauth unchecked → no oauth field in payload', async () => {
    render(<ToolsPanel />)
    fireEvent.click(screen.getByRole('button', { name: /添加服务器|add a server/i }))
    const names = screen.getAllByPlaceholderText('server')
    fireEvent.change(names[0], { target: { value: 'plain' } })
    fireEvent.click(screen.getByRole('button', { name: /保存工具设置|save tool settings/i }))
    await new Promise((r) => setTimeout(r, 30))
    const payload = savePayloads()[savePayloads().length - 1]!
    expect(payload.mcpServers[0]).not.toHaveProperty('oauth')
  })

  it('filled client_secret is included in payload', async () => {
    render(<ToolsPanel />)
    fireEvent.click(screen.getByRole('button', { name: /添加服务器|add a server/i }))
    const names = screen.getAllByPlaceholderText('server')
    fireEvent.change(names[0], { target: { value: 'sec' } })
    fireEvent.click(screen.getByRole('checkbox', { name: /http/i }))
    const urlInput = screen.getByPlaceholderText(/http:\/\/|https?:\/\//i)
    fireEvent.change(urlInput, { target: { value: 'http://x.local/mcp' } })
    fireEvent.click(screen.getByRole('checkbox', { name: /oauth/i }))
    const fields = screen.getAllByPlaceholderText(/token_url|client_id|secret|scopes/i) as HTMLInputElement[]
    fireEvent.change(fields[0], { target: { value: 'http://x.local/token' } })
    fireEvent.change(fields[1], { target: { value: 'cid' } })
    fireEvent.change(fields[2], { target: { value: 'shhh' } })
    fireEvent.click(screen.getByRole('button', { name: /保存工具设置|save tool settings/i }))
    await new Promise((r) => setTimeout(r, 30))
    const payload = savePayloads()[savePayloads().length - 1]!
    expect(payload.mcpServers[0].oauth.client_secret).toBe('shhh')
  })
})
