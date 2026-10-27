# Round 11 批2 暗色主题 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** light/dark/system 三态主题切换：ui-store 持久化 + Header 循环按钮 + index.html 无闪烁预注入。

**Architecture:** `darkMode:'class'` 已就绪且组件均已带 dark: 样式；只需状态与切换逻辑。store 管三态并写 `documentElement.classList`；index.html 内联脚本在首帧前读 localStorage 消除闪烁；main.tsx 启动时 initTheme（含 system 媒体查询监听）。

**Tech Stack:** zustand 5、lucide-react (Sun/Moon/Monitor)、vitest+jsdom。

---

### Task 1: ui-store theme 三态 + applyTheme/initTheme（TDD）

**Files:**
- Modify: `frontend/src/store/ui-store.ts`
- Test: `frontend/src/store/ui-store.test.ts`

- [ ] **Step 1: 写失败测试（追加到 ui-store.test.ts）**

```ts
import { vi, afterEach } from 'vitest' // 追加 import

const KEY = 'rivus-theme'

function mockMatchMedia(dark: boolean) {
  vi.stubGlobal('matchMedia', (q: string) => ({
    matches: dark && q.includes('dark'),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  }))
}

describe('theme', () => {
  afterEach(() => {
    localStorage.removeItem(KEY)
    vi.unstubAllGlobals()
    document.documentElement.classList.remove('dark')
  })

  it('default theme is system', () => {
    useUiStore.setState({ theme: 'system' }) // beforeEach 已重置
    expect(useUiStore.getState().theme).toBe('system')
  })

  it('setTheme dark adds class and persists', () => {
    useUiStore.getState().setTheme('dark')
    expect(useUiStore.getState().theme).toBe('dark')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    expect(localStorage.getItem(KEY)).toBe('dark')
  })

  it('setTheme light removes class', () => {
    document.documentElement.classList.add('dark')
    useUiStore.getState().setTheme('light')
    expect(document.documentElement.classList.contains('dark')).toBe(false)
    expect(localStorage.getItem(KEY)).toBe('light')
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
    const s = useUiStore.getState()
    s.setTheme(s.cycleTheme()) // 或 s.cycle() — 以实现为准，见 Step 3
    expect(useUiStore.getState().theme).toBe('dark')
  })

  it('initTheme reads storage and applies', () => {
    localStorage.setItem(KEY, 'dark')
    mockMatchMedia(false)
    useUiStore.getState().initTheme()
    expect(useUiStore.getState().theme).toBe('dark')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
  })
})
```
（beforeEach 的 setState 需补 `theme: 'system'`。cycleTheme 用返回下一个值的纯函数式 `nextTheme(t)` + `setTheme` 组合，避免 store 塞循环逻辑——最终形态见实现。）

- [ ] **Step 2: 运行确认 RED**

```bash
cd frontend && npx vitest run src/store/ui-store.test.ts
```
Expected: FAIL（theme 不存在 / 类不切换）。

- [ ] **Step 3: 实现 ui-store.ts**

```ts
import { create } from 'zustand'
import type { PollStatus } from '@/lib/poller'

export type Theme = 'light' | 'dark' | 'system'
const THEME_KEY = 'rivus-theme'

export function nextTheme(t: Theme): Theme {
  return t === 'light' ? 'dark' : t === 'dark' ? 'system' : 'light'
}

// JS 守卫：SSR/jsdom 缺 matchMedia 时降级为 light
export function isDark(t: Theme): boolean {
  if (t === 'dark') return true
  if (t !== 'system') return false
  return typeof window !== 'undefined' && !!window.matchMedia?.('(prefers-color-scheme: dark)').matches
}

export function applyTheme(t: Theme) {
  if (typeof document === 'undefined') return
  document.documentElement.classList.toggle('dark', isDark(t))
}

interface UiState {
  sseStatus: PollStatus
  apiReachable: boolean
  theme: Theme
  setSseStatus: (s: PollStatus) => void
  setApiReachable: (b: boolean) => void
  setTheme: (t: Theme) => void
  cycleTheme: () => void
  initTheme: () => void
}

export const useUiStore = create<UiState>((set, get) => ({
  sseStatus: 'stopped',
  apiReachable: true,
  theme: 'system',
  setSseStatus: (s) => set({ sseStatus: s }),
  setApiReachable: (b) => set({ apiReachable: b }),
  setTheme: (t) => {
    try { localStorage.setItem(THEME_KEY, t) } catch { /* 隐私模式等，忽略 */ }
    applyTheme(t)
    set({ theme: t })
  },
  cycleTheme: () => get().setTheme(nextTheme(get().theme)),
  initTheme: () => {
    let t: Theme = 'system'
    try {
      const stored = localStorage.getItem(THEME_KEY)
      if (stored === 'light' || stored === 'dark' || stored === 'system') t = stored
    } catch { /* 忽略 */ }
    applyTheme(t)
    set({ theme: t })
    if (typeof window !== 'undefined' && typeof window.matchMedia === 'function') {
      window.matchMedia('(prefers-color-scheme: dark)').addEventListener?.('change', () => {
        if (get().theme === 'system') applyTheme('system')
      })
    }
  },
}))
```
（Step 1 测试中 cycleTheme 用法相应改为 `s.cycleTheme()`。）

- [ ] **Step 4: GREEN**

```bash
cd frontend && npx vitest run src/store/ui-store.test.ts
```
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add frontend/src/store/ui-store.ts frontend/src/store/ui-store.test.ts
git commit -m "feat(frontend): theme tri-state in ui-store with persistence and applyTheme"
```

### Task 2: Header 循环按钮 + index.html 无闪烁 + main.tsx 接线（TDD）

**Files:**
- Modify: `frontend/src/components/Header.tsx`
- Modify: `frontend/index.html`（去掉硬编码 `class="dark"` 与 body `bg-zinc-950`，加内联脚本）
- Modify: `frontend/src/main.tsx`（`useUiStore.getState().initTheme()`）
- Test: `frontend/src/components/Header.test.tsx`

- [ ] **Step 1: Header 失败测试（追加）**

```tsx
it('cycles theme on button click', async () => {
  useUiStore.setState({ theme: 'light' })
  const { user } = render(<Header />)
  await user.click(screen.getByRole('button', { name: '切换主题' }))
  expect(useUiStore.getState().theme).toBe('dark')
  expect(document.documentElement.classList.contains('dark')).toBe(true)
})
```
（沿用该文件既有的 render/user 工具；若文件用 fireEvent 则用 fireEvent.click。afterEach 清 localStorage + classList。）

- [ ] **Step 2: RED → 实现 Header**

```tsx
import { Sun, Moon, Monitor } from 'lucide-react'
// 组件内：
const theme = useUiStore((s) => s.theme)
// 按钮插到 运行历史 按钮前：
const label = theme === 'light' ? '浅色' : theme === 'dark' ? '深色' : '跟随系统'
const ThemeIcon = theme === 'light' ? Sun : theme === 'dark' ? Moon : Monitor
<button
  type="button"
  aria-label="切换主题"
  title={`主题：${label}`}
  onClick={() => useUiStore.getState().cycleTheme()}
  className="flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
>
  <ThemeIcon className="h-4 w-4" />
</button>
```

- [ ] **Step 3: index.html 无闪烁脚本**

```html
<html lang="en">
  <head>
    ...（原 meta/title 不动）
    <script>
      (function () {
        try {
          var t = localStorage.getItem('rivus-theme') || 'system'
          var dark = t === 'dark' || (t === 'system' && window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches)
          document.documentElement.classList.toggle('dark', dark)
        } catch (e) {}
      })()
    </script>
  </head>
  <body class="bg-background">
```
（删除 `<html class="dark">` 的 dark；body `bg-zinc-950` → `bg-background` 适配双主题。）

- [ ] **Step 4: main.tsx 调 initTheme**

```tsx
import { useUiStore } from '@/store/ui-store'
useUiStore.getState().initTheme() // createRoot render 之前
```

- [ ] **Step 5: GREEN + 回归 + lint**

```bash
cd frontend && npx vitest run && npm run lint && npx tsc --noEmit
```
Expected: 全绿（≥152 tests）。

- [ ] **Step 6: Commit**

```bash
git add frontend/src/components/Header.tsx frontend/src/components/Header.test.tsx frontend/index.html frontend/src/main.tsx
git commit -m "feat(frontend): theme toggle button and no-flash pre-hydration script"
```

---

## Self-review notes

- index.html 内联脚本用 try/catch 包 localStorage（隐私模式抛异常）。
- jsdom 无 matchMedia：isDark 用 `window.matchMedia?.` 可选链守卫；测试通过 vi.stubGlobal mock。
- 现状 html 硬编码 dark：脚本删除该 class 后首帧由脚本决定；无 JS 环境退化为 light（可接受）。
- system 模式监听在 initTheme 注册一次；重复调用 initTheme 会叠加监听器——main.tsx 只调一次，风险可控（不改 zustand 生命周期，保持最小）。
