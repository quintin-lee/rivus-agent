import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { useUiStore } from '@/store/ui-store'

const THEME_KEY = 'rivus-theme'

function mockMatchMedia(dark: boolean) {
  vi.stubGlobal('matchMedia', (q: string) => ({
    matches: dark && q.includes('dark'),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  }))
}

describe('ui-store', () => {
  beforeEach(() => {
    useUiStore.setState({
      sseStatus: 'stopped',
      apiReachable: true,
      theme: 'system',
    })
  })

  it('setSseStatus updates sseStatus', () => {
    useUiStore.getState().setSseStatus('polling')
    expect(useUiStore.getState().sseStatus).toBe('polling')
  })

  it('setApiReachable updates apiReachable', () => {
    useUiStore.getState().setApiReachable(false)
    expect(useUiStore.getState().apiReachable).toBe(false)
  })
})

describe('theme', () => {
  beforeEach(() => {
    useUiStore.setState({ theme: 'system' })
  })

  afterEach(() => {
    localStorage.removeItem(THEME_KEY)
    vi.unstubAllGlobals()
    document.documentElement.classList.remove('dark')
  })

  it('default theme is system', () => {
    expect(useUiStore.getState().theme).toBe('system')
  })

  it('setTheme dark adds class and persists', () => {
    useUiStore.getState().setTheme('dark')
    expect(useUiStore.getState().theme).toBe('dark')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    expect(localStorage.getItem(THEME_KEY)).toBe('dark')
  })

  it('setTheme light removes class', () => {
    document.documentElement.classList.add('dark')
    useUiStore.getState().setTheme('light')
    expect(document.documentElement.classList.contains('dark')).toBe(false)
    expect(localStorage.getItem(THEME_KEY)).toBe('light')
  })

  it('system follows matchMedia', () => {
    mockMatchMedia(true)
    useUiStore.getState().setTheme('system')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    mockMatchMedia(false)
    useUiStore.getState().setTheme('system')
    expect(document.documentElement.classList.contains('dark')).toBe(false)
  })

  it('cycleTheme light->dark->system->light', () => {
    useUiStore.setState({ theme: 'light' })
    useUiStore.getState().cycleTheme()
    expect(useUiStore.getState().theme).toBe('dark')
    useUiStore.getState().cycleTheme()
    expect(useUiStore.getState().theme).toBe('system')
    useUiStore.getState().cycleTheme()
    expect(useUiStore.getState().theme).toBe('light')
  })

  it('initTheme reads storage and applies', () => {
    localStorage.setItem(THEME_KEY, 'dark')
    mockMatchMedia(false)
    useUiStore.getState().initTheme()
    expect(useUiStore.getState().theme).toBe('dark')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
  })
})
