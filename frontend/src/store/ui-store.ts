import { create } from 'zustand'
import type { PollStatus } from '@/lib/poller'

export type Theme = 'light' | 'dark' | 'system'
const THEME_KEY = 'rivus-theme'

export function nextTheme(t: Theme): Theme {
  return t === 'light' ? 'dark' : t === 'dark' ? 'system' : 'light'
}

// 环境守卫：jsdom/SSR 缺 matchMedia 时 system 降级为 light
export function isDark(t: Theme): boolean {
  if (t === 'dark') return true
  if (t !== 'system') return false
  return typeof window !== 'undefined' && !!window.matchMedia?.('(prefers-color-scheme: dark)').matches
}

export function applyTheme(t: Theme) {
  if (typeof document === 'undefined') return
  document.documentElement.classList.toggle('dark', isDark(t))
}

export type Lang = 'zh' | 'en'
const LANG_KEY = 'rivus-lang'

interface UiState {
  sseStatus: PollStatus
  apiReachable: boolean
  theme: Theme
  lang: Lang
  setSseStatus: (s: PollStatus) => void
  setApiReachable: (b: boolean) => void
  setTheme: (t: Theme) => void
  cycleTheme: () => void
  initTheme: () => void
  setLang: (l: Lang) => void
  toggleLang: () => void
  initLang: () => void
}

export const useUiStore = create<UiState>((set, get) => ({
  sseStatus: 'stopped',
  apiReachable: true,
  theme: 'system',
  lang: 'zh',
  setSseStatus: (s) => set({ sseStatus: s }),
  setApiReachable: (b) => set({ apiReachable: b }),
  setTheme: (t) => {
    try {
      localStorage.setItem(THEME_KEY, t)
    } catch {
      // 隐私模式等存储不可用场景，仅内存态生效
    }
    applyTheme(t)
    set({ theme: t })
  },
  cycleTheme: () => get().setTheme(nextTheme(get().theme)),
  initTheme: () => {
    let t: Theme = 'system'
    try {
      const stored = localStorage.getItem(THEME_KEY)
      if (stored === 'light' || stored === 'dark' || stored === 'system') t = stored
    } catch {
      // 忽略
    }
    applyTheme(t)
    set({ theme: t })
    if (typeof window !== 'undefined' && typeof window.matchMedia === 'function') {
      window
        .matchMedia('(prefers-color-scheme: dark)')
        .addEventListener?.('change', () => {
          if (get().theme === 'system') applyTheme('system')
        })
    }
  },
  setLang: (l) => {
    try {
      localStorage.setItem(LANG_KEY, l)
    } catch {
      // 存储不可用时仅内存态生效
    }
    set({ lang: l })
  },
  toggleLang: () => get().setLang(get().lang === 'zh' ? 'en' : 'zh'),
  initLang: () => {
    let l: Lang = 'zh'
    try {
      const stored = localStorage.getItem(LANG_KEY)
      if (stored === 'zh' || stored === 'en') l = stored
    } catch {
      // 忽略
    }
    set({ lang: l })
  },
}))
