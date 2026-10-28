# Round-13 批3: Frontend i18n Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Minimal i18n mechanism (zh/en dict + `t()` + ui-store lang state + Header toggle) and migrate the four dialog/list components plus one `status.ts` sample to it.

**Architecture:** No react-i18next — a flat dictionary module `src/i18n/index.ts` exports `t(key, params?)` that reads the active language from the zustand ui-store (`useUiStore.getState().lang`), falls back en→zh→key, and substitutes `{name}` placeholders. Components subscribe to `lang` so toggling re-renders. Default `zh` keeps every existing test's Chinese assertions valid.

**Tech Stack:** React 18 + zustand + vitest + @testing-library/react (existing stack; zero new deps).

**Spec:** docs/superpowers/specs/2026-10-28-round13-ci-backup-i18n-design.md §批3

**YAGNI (from spec):** no react-i18next dependency, no route-level switching, no date/relTime localization beyond existing relative strings, no index.html pre-hydration script for lang (default zh, no flash concern), Header theme strings untouched.

---

## Task 1: i18n dictionary module + t()

**Files:**
- Create: `frontend/src/i18n/index.ts`
- Test: `frontend/src/i18n/i18n.test.ts`

- [ ] **Step 1: Write the failing test**

```ts
// frontend/src/i18n/i18n.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { t, setI18nLangForTest } from '@/i18n'
import { useUiStore } from '@/store/ui-store'

describe('i18n t()', () => {
  beforeEach(() => {
    localStorage.clear()
    useUiStore.setState({ lang: 'zh' })
  })

  it('returns zh string under default lang', () => {
    expect(t('common.cancel')).toBe('取消')
  })

  it('returns en string after lang switch', () => {
    useUiStore.setState({ lang: 'en' })
    expect(t('common.cancel')).toBe('Cancel')
  })

  it('substitutes {name} params', () => {
    expect(t('sessionlist.deleteConfirmBody', { count: 3 })).toContain('3 个 Run')
  })

  it('falls back to zh when en key missing', () => {
    setI18nLangForTest('en')
    expect(t('i18n.test.zhonly')).toBe('仅中文')
  })

  it('returns the key itself when unknown', () => {
    expect(t('no.such.key')).toBe('no.such.key')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd frontend && npx vitest run src/i18n/i18n.test.ts`
Expected: FAIL — cannot resolve `@/i18n`.

- [ ] **Step 3: Implement the module**

```ts
// frontend/src/i18n/index.ts
import { useUiStore, type Lang } from '@/store/ui-store'

// en 缺键回退 zh；zh 缺键回退键名本身。
const zh: Record<string, string> = {
  'common.cancel': '取消',
  'common.delete': '删除',
  'common.deleting': '删除中…',
  'common.retry': '重试',
  'common.refresh': '刷新',
  'common.loadFailed': '加载失败',
  'common.loading': '加载中…',

  'time.justNow': '刚刚',
  'time.minAgo': '{n} 分钟前',
  'time.hourAgo': '{n} 小时前',
  'time.dayAgo': '{n} 天前',

  'sessionlist.title': '会话',
  'sessionlist.refresh': '刷新会话',
  'sessionlist.newPlaceholder': '新会话标题…',
  'sessionlist.noSessions': '暂无会话',
  'sessionlist.noRuns': '暂无运行',
  'sessionlist.memoriesTitle': '会话记忆',
  'sessionlist.deleteTitle': '删除会话',
  'sessionlist.deleteConfirmTitle': '删除会话？',
  'sessionlist.deleteConfirmBody':
    '该会话及名下 {count} 个 Run（含事件与审批记录）将被一并删除，不可恢复。',
  'sessionlist.deleteActiveWarn': '存在运行中的任务，请先取消后再删除。',

  'memories.title': '会话记忆',
  'memories.description': 'Agent 在本会话中沉淀的记忆（最新在前）。',
  'memories.filterPlaceholder': '过滤记忆…',
  'memories.empty': '（无记忆）',

  'runs.title': '运行历史',
  'runs.description': '跨会话的全部顶层运行（最新在前，不含子 agent 运行）。',
  'runs.empty': '（无运行记录）',
  'runs.sessionLabel': '会话',

  'status.queued': '排队中',
  'status.running': '运行中',
  'status.waiting_approval': '待审批',
  'status.paused': '已暂停',
  'status.succeeded': '成功',
  'status.failed': '失败',
  'status.cancelled': '已取消',
  'status.timed_out': '超时',
  'status.budgetUsage': '用量 工具 {tool}/模型 {model}',
  'status.budgetUpdated': '用量更新',

  // 仅供测试的 zh-only 键
  'i18n.test.zhonly': '仅中文',
}

const en: Record<string, string> = {
  'common.cancel': 'Cancel',
  'common.delete': 'Delete',
  'common.deleting': 'Deleting…',
  'common.retry': 'Retry',
  'common.refresh': 'Refresh',
  'common.loadFailed': 'Failed to load',
  'common.loading': 'Loading…',

  'time.justNow': 'just now',
  'time.minAgo': '{n} min ago',
  'time.hourAgo': '{n} h ago',
  'time.dayAgo': '{n} d ago',

  'sessionlist.title': 'Sessions',
  'sessionlist.refresh': 'Refresh sessions',
  'sessionlist.newPlaceholder': 'New session title…',
  'sessionlist.noSessions': 'No sessions',
  'sessionlist.noRuns': 'No runs',
  'sessionlist.memoriesTitle': 'Session memories',
  'sessionlist.deleteTitle': 'Delete session',
  'sessionlist.deleteConfirmTitle': 'Delete session?',
  'sessionlist.deleteConfirmBody':
    'This session and its {count} runs (including events and approvals) will be deleted permanently.',
  'sessionlist.deleteActiveWarn': 'Active runs exist — cancel them before deleting.',

  'memories.title': 'Session Memories',
  'memories.description': 'Memories the agent accumulated in this session (newest first).',
  'memories.filterPlaceholder': 'Filter memories…',
  'memories.empty': '(no memories)',

  'runs.title': 'Run History',
  'runs.description': 'All top-level runs across sessions (newest first, sub-agent runs excluded).',
  'runs.empty': '(no runs)',
  'runs.sessionLabel': 'Session',

  'status.queued': 'Queued',
  'status.running': 'Running',
  'status.waiting_approval': 'Awaiting approval',
  'status.paused': 'Paused',
  'status.succeeded': 'Succeeded',
  'status.failed': 'Failed',
  'status.cancelled': 'Cancelled',
  'status.timed_out': 'Timed out',
  'status.budgetUsage': 'Usage tool {tool}/model {model}',
  'status.budgetUpdated': 'Usage updated',

  // i18n.test.zhonly deliberately absent from en — exercises fallback.
}

const dict: Record<Lang, Record<string, string>> = { zh, en }

export function t(key: string, params?: Record<string, string | number>): string {
  const lang = useUiStore.getState().lang
  let s = dict[lang][key] ?? dict.zh[key] ?? key
  if (params) {
    for (const [name, value] of Object.entries(params)) {
      s = s.replaceAll(`{${name}}`, String(value))
    }
  }
  return s
}

/** 仅测试用：绕过 store 直接改当前语言（store 之外的回退验证）。 */
export function setI18nLangForTest(lang: Lang) {
  useUiStore.setState({ lang })
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd frontend && npx vitest run src/i18n/i18n.test.ts`
Expected: PASS (5 tests). Note: `lang` does not exist on the store yet — Task 2 adds it. To keep this step green standalone, Task 2 is executed immediately after; run this test again after Task 2. If the store lacks `lang`, `useUiStore.getState().lang` is `undefined` → `dict[undefined]` → zh fallback via `??` — tests still pass because zh lookups work.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/i18n
git commit -m "feat(frontend): minimal i18n dictionary with t(), params and en->zh fallback"
```

---

## Task 2: ui-store lang state + main.tsx init

**Files:**
- Modify: `frontend/src/store/ui-store.ts` (theme pattern, add lang beside it)
- Modify: `frontend/src/main.tsx:7` (add initLang call)
- Test: `frontend/src/store/ui-store.test.ts` (append `describe('lang')` suite)

- [ ] **Step 1: Write the failing test** — append to `ui-store.test.ts`:

```ts
const LANG_KEY = 'rivus-lang'

describe('lang', () => {
  beforeEach(() => {
    localStorage.clear()
    useUiStore.setState({ lang: 'zh' })
  })

  it('default lang is zh', () => {
    expect(useUiStore.getState().lang).toBe('zh')
  })

  it('setLang persists to localStorage', () => {
    useUiStore.getState().setLang('en')
    expect(useUiStore.getState().lang).toBe('en')
    expect(localStorage.getItem(LANG_KEY)).toBe('en')
  })

  it('toggleLang flips zh<->en', () => {
    useUiStore.getState().toggleLang()
    expect(useUiStore.getState().lang).toBe('en')
    useUiStore.getState().toggleLang()
    expect(useUiStore.getState().lang).toBe('zh')
  })

  it('initLang reads storage', () => {
    localStorage.setItem(LANG_KEY, 'en')
    useUiStore.getState().initLang()
    expect(useUiStore.getState().lang).toBe('en')
  })

  it('initLang ignores invalid stored value', () => {
    localStorage.setItem(LANG_KEY, 'fr')
    useUiStore.getState().initLang()
    expect(useUiStore.getState().lang).toBe('zh')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd frontend && npx vitest run src/store/ui-store.test.ts`
Expected: FAIL — `lang`/`setLang`/`toggleLang`/`initLang` undefined (type error + runtime).

- [ ] **Step 3: Implement** — in `ui-store.ts`:

Add after the theme block (keep theme code untouched):

```ts
export type Lang = 'zh' | 'en'
const LANG_KEY = 'rivus-lang'
```

Extend `UiState`:

```ts
  lang: Lang
  setLang: (l: Lang) => void
  toggleLang: () => void
  initLang: () => void
```

Add store methods (after `initTheme`):

```ts
  lang: 'zh',
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
```

In `main.tsx` after `useUiStore.getState().initTheme()` add:

```ts
useUiStore.getState().initLang()
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd frontend && npx vitest run src/store/ui-store.test.ts src/i18n/i18n.test.ts`
Expected: PASS (theme suite + 5 lang tests + 5 i18n tests).

- [ ] **Step 5: Commit**

```bash
git add frontend/src/store/ui-store.ts frontend/src/store/ui-store.test.ts frontend/src/main.tsx
git commit -m "feat(frontend): ui-store lang state (zh/en) with persistence and init"
```

---

## Task 3: Header 中/EN toggle

**Files:**
- Modify: `frontend/src/components/Header.tsx` (add `LangToggle` beside `ThemeToggle`)
- Test: `frontend/src/components/Header.test.tsx` (append describe)

- [ ] **Step 1: Write the failing test** — append to `Header.test.tsx`:

```tsx
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd frontend && npx vitest run src/components/Header.test.tsx`
Expected: FAIL — button `切换语言` not found.

- [ ] **Step 3: Implement** — in `Header.tsx` add beside `ThemeToggle`:

```tsx
function LangToggle() {
  const lang = useUiStore((s) => s.lang)
  return (
    <button
      type="button"
      aria-label="切换语言"
      title={lang === 'zh' ? '语言：中文' : 'Language: English'}
      onClick={() => useUiStore.getState().toggleLang()}
      className="flex h-7 min-w-7 items-center justify-center rounded-md px-1 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
    >
      {lang === 'zh' ? 'EN' : '中'}
    </button>
  )
}
```

Render `<LangToggle />` immediately before `<ThemeToggle />` in the toolbar row.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd frontend && npx vitest run src/components/Header.test.tsx`
Expected: PASS (existing 2 + new 1).

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/Header.tsx frontend/src/components/Header.test.tsx
git commit -m "feat(frontend): header zh/en language toggle button"
```

---

## Task 4: Component migration + status.ts sample + regression + docs

**Files:**
- Modify: `frontend/src/components/SessionList.tsx`
- Modify: `frontend/src/components/MemoriesDialog.tsx`
- Modify: `frontend/src/components/RunsHistoryDialog.tsx`
- Modify: `frontend/src/lib/status.ts` (budget.consumed two Chinese lines only)
- Test: `frontend/src/components/MemoriesDialog.test.tsx` (append en sample)
- Modify: `frontend/README.md` + `frontend/README.en.md` (i18n note)
- Modify: spec (deviation write-back)

Migration rules (mechanical):
1. `import { t } from '@/i18n'` + `const lang = useUiStore((s) => s.lang)` in each migrated component (subscription drives re-render on toggle).
2. Replace string literals with `t('key')` / `t('key', { params })`; `relTime` uses `t('time.*', { n })`.
3. RunsHistoryDialog `statusLabel` map → `t('status.' + r.status)` (all 8 statuses in dict; unknown status still falls back to `r.status` via the existing `?? r.status` pattern — keep `statusLabel` deleted and use `{runStatusClasses[r.status as RunStatus] ?? ''}` for class, `{r.status in statusKeys ? t('status.' + r.status) : r.status}` for label; simpler: keep a local `const known = (s: string) => s in statuSet` — implementation detail, either way tests assert zh labels unchanged).
   Chosen minimal form: `const label = STATUSES.has(r.status) ? t(`status.${r.status}`) : r.status` with `const STATUSES = new Set(['queued','running','waiting_approval','paused','succeeded','failed','cancelled','timed_out'])`.
4. status.ts: only the two `budget.consumed` branches:
   - `t('status.budgetUsage', { tool: String(p.tool_calls), model: String(p.model_calls ?? 0) })`
   - `t('status.budgetUpdated')`
   (Import `t` from `@/i18n`; eventSummary callers re-render on their own schedule — under default zh output is byte-identical, so existing tests are unaffected.)

- [ ] **Step 1: Append failing en-sample test to `MemoriesDialog.test.tsx`**

```tsx
describe('MemoriesDialog i18n', () => {
  beforeEach(() => {
    localStorage.clear()
    useUiStore.setState({ lang: 'en' })
  })

  it('renders en empty state', async () => {
    vi.mocked(api.listMemories).mockResolvedValue([])
    render(<MemoriesDialog sessionId="s1" sessionTitle="t" open onOpenChange={() => {}} />)
    await waitFor(() => expect(screen.getByText('(no memories)')).toBeTruthy())
  })
})
```

(Add `import { useUiStore } from '@/store/ui-store'`.)

- [ ] **Step 2: Run test to verify it fails**

Run: `cd frontend && npx vitest run src/components/MemoriesDialog.test.tsx`
Expected: FAIL — empty state still `（无记忆）`.

- [ ] **Step 3: Migrate the three components + status.ts per the rules above.**

- [ ] **Step 4: Full frontend verification**

Run: `cd frontend && npx tsc --noEmit && npm run lint && npx vitest run`
Expected: tsc 0 errors; lint clean; all tests pass (existing Chinese assertions unaffected under default zh).

- [ ] **Step 5: Backend regression (no backend changes — sanity)**

Run: `cd backend && go build ./... && go test ./internal/... -count=1`
Expected: all ok.

- [ ] **Step 6: Docs + spec deviation write-back + commit**

- `frontend/README.md` commands line notes += `i18n：zh/en 双语（Header 中/EN 切换，localStorage rivus-lang）`;
- `frontend/README.en.md` mirror: `i18n: zh/en locales (Header toggle, localStorage rivus-lang)`;
- Spec `docs/superpowers/specs/2026-10-28-round13-ci-backup-i18n-design.md` §批3 append deviation blockquote: t() reads lang from ui-store via getState (not a module-level variable) so a single source of truth; en dict missing `i18n.test.zhonly` exercises fallback; status.ts migrated only the budget.consumed lines (eventSummary callers re-render on their own schedule, zh output byte-identical); statusLabel map replaced by `status.*` dict keys with unknown-status fallback to raw status.

```bash
git add frontend/src frontend/README.md frontend/README.en.md docs/superpowers/specs/2026-10-28-round13-ci-backup-i18n-design.md
git commit -m "feat(frontend): migrate dialogs and status sample to i18n (zh/en)"
```

---

## Self-review notes (pre-execution)

- **Re-render correctness:** every migrated component subscribes `useUiStore((s) => s.lang)`; toggling flips state → subscribed components re-render → `t()` re-evaluates. Components rendering `eventSummary` (chat area) are NOT migrated per spec — their output only changes on new events; documented as accepted limitation.
- **Default-zh invariant:** all existing tests assert Chinese strings; default lang zh → untouched behavior. Verified by full vitest run.
- **No import cycle:** i18n imports ui-store; ui-store imports nothing from i18n.
- **zustand setState in tests:** `useUiStore.setState({ lang: 'en' })` partial-merge is valid zustand API (used by existing theme tests via setState({theme})).
- **`replaceAll`**: ES2021 — project targets modern browsers (vite default), TS lib includes it; if tsc complains, switch to split/join.
