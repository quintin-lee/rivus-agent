import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { SettingsDialog } from './SettingsDialog'

const { getModelSettings, updateModelSettings } = vi.hoisted(() => ({
  getModelSettings: vi.fn().mockResolvedValue({
    provider: 'openai_compat',
    base_url: 'https://api.openai.com/v1',
    model: 'gpt-4o-mini',
    api_key_set: true,
  }),
  updateModelSettings: vi.fn().mockResolvedValue(undefined),
}))

vi.mock('@/lib/api', () => ({ api: { getModelSettings, updateModelSettings }, ApiError: class extends Error {} }))

describe('SettingsDialog', () => {
  beforeEach(() => {
    getModelSettings.mockClear()
    updateModelSettings.mockClear()
  })

  it('opens and shows current settings with key badge', async () => {
    render(<SettingsDialog />)
    fireEvent.click(screen.getByTitle('模型设置'))
    await waitFor(() => expect(getModelSettings).toHaveBeenCalled())
    expect(await screen.findByDisplayValue('https://api.openai.com/v1')).toBeInTheDocument()
    expect(screen.getByText('已设置')).toBeInTheDocument()
  })

  it('saves only filled fields, empty key keeps existing', async () => {
    render(<SettingsDialog />)
    fireEvent.click(screen.getByTitle('模型设置'))
    await screen.findByDisplayValue('gpt-4o-mini')
    fireEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(updateModelSettings).toHaveBeenCalled())
    const patch = updateModelSettings.mock.calls[0][0] as Record<string, string | undefined>
    expect(patch.model).toBe('gpt-4o-mini')
    expect(patch.api_key).toBeUndefined()
    expect(await screen.findByText('已保存，新提交的 Run 生效')).toBeInTheDocument()
  })
})
