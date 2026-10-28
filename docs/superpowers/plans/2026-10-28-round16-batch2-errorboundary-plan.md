# Round-16 批2 前端 ErrorBoundary Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为前端加一个 class 组件 ErrorBoundary，挂在 App 根部与聊天消息区两处，崩溃时显示可重试的兜底 UI。

**Architecture:** 单文件 class 组件（React 官方错误边界唯一方式，无 hooks 依赖）；`getDerivedStateFromError` 切换兜底渲染，`componentDidCatch` 仅 `console.error`；「重试」按钮清空 error 状态触发重渲染。i18n 复用 `t()`（新增 `error.boundary` 键，按钮复用已有 `common.retry`）。

**Tech Stack:** React 18 class component、vitest + @testing-library/react、zustand ui-store（lang）、既有 i18n `t()`。

**自检要点（self-review notes）:**
- React StrictMode 下渲染抛错会被 React 捕获并以 `console.error` 输出——测试里 mock `console.error` 防噪。
- Thrower 测试组件：通过模块级 flag（或 React state）控制是否抛错；点击「重试」前先关掉 throw 开关，断言子内容重新渲染。
- `componentDidCatch` 里不得 setState（用 `getDerivedStateFromError`）。
- lint：`@typescript-eslint/no-unused-vars` 注意 `componentDidCatch(error)` 参数保留但仅用于 console.error。
- 两处挂载点：`main.tsx` 包 `<App />`（根兜底）；`ChatMessageList.tsx` 返回 JSX 外层包一层（局部兜底，重试只重挂消息区）。

---

### Task 1: ErrorBoundary 组件 + 单测（TDD）

**Files:**
- Create: `frontend/src/components/ErrorBoundary.tsx`
- Create: `frontend/src/components/ErrorBoundary.test.tsx`
- Modify: `frontend/src/i18n/index.ts`（新增 `error.boundary` 键）

- [ ] **Step 1: 写失败测试**

```tsx
// frontend/src/components/ErrorBoundary.test.tsx
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { useState } from 'react'
import { ErrorBoundary } from './ErrorBoundary'

function Thrower({ active }: { active: boolean }) {
  if (active) throw new Error('boom')
  return <div>child-content</div>
}

describe('ErrorBoundary', () => {
  beforeEach(() => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })

  it('renders fallback when child throws, recovers on retry', () => {
    let throwing = true
    const { rerender, getByText, queryByText } = render(
      <ErrorBoundary>
        <Thrower active={throwing} />
      </ErrorBoundary>,
    )
    expect(getByText('页面出错了，请重试')).toBeTruthy()
    throwing = false
    fireEvent.click(getByText('重试'))
    rerender(
      <ErrorBoundary>
        <Thrower active={throwing} />
      </ErrorBoundary>,
    )
    expect(queryByText('child-content')).toBeTruthy()
  })

  it('renders children normally when nothing throws', () => {
    render(
      <ErrorBoundary>
        <div>plain-child</div>
      </ErrorBoundary>,
    )
    expect(screen.getByText('plain-child')).toBeTruthy()
  })
})
```

先给 i18n 加键（测试断言依赖 zh 文案）：

```ts
// i18n/index.ts zh dict:
'error.boundary': '页面出错了，请重试',
// en dict:
'error.boundary': 'Something went wrong. Please retry.',
```

- [ ] **Step 2: 运行确认失败**

Run: `cd frontend && npx vitest run src/components/ErrorBoundary.test.tsx`
Expected: FAIL（`./ErrorBoundary` 不存在 / 无 fallback 文案）

- [ ] **Step 3: 最小实现**

```tsx
// frontend/src/components/ErrorBoundary.tsx
import React from 'react'
import { AlertTriangle } from 'lucide-react'
import { t } from '@/i18n'

interface Props {
  children: React.ReactNode
}

interface State {
  error: Error | null
}

export class ErrorBoundary extends React.Component<Props, State> {
  state: State = { error: null }

  static getDerivedStateFromError(error: Error): State {
    return { error }
  }

  componentDidCatch(error: Error) {
    console.error('react render error:', error)
  }

  private reset = () => {
    this.setState({ error: null })
  }

  render() {
    if (this.state.error) {
      return (
        <div className="flex flex-col items-center gap-3 rounded-lg border border-border bg-card/50 p-8 text-center">
          <AlertTriangle className="h-5 w-5 text-destructive" aria-hidden />
          <p className="text-sm font-medium">{t('error.boundary')}</p>
          <button
            type="button"
            onClick={this.reset}
            className="rounded-md border border-border px-3 py-1.5 text-sm hover:bg-accent"
          >
            {t('common.retry')}
          </button>
        </div>
      )
    }
    return this.props.children
  }
}
```

- [ ] **Step 4: 运行确认通过**

Run: `cd frontend && npx vitest run src/components/ErrorBoundary.test.tsx && npx tsc --noEmit && npm run lint`
Expected: 2 tests PASS；tsc 0 errors；lint OK

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/ErrorBoundary.tsx frontend/src/components/ErrorBoundary.test.tsx frontend/src/i18n/index.ts
git commit -m "feat(frontend): error boundary component with retry"
```

---

### Task 2: 双挂载点 + 回归

**Files:**
- Modify: `frontend/src/main.tsx`
- Modify: `frontend/src/components/ChatMessageList.tsx`

- [ ] **Step 1: main.tsx 根部包裹**

```tsx
import { ErrorBoundary } from '@/components/ErrorBoundary'
// render 内：
<ErrorBoundary>
  <App />
</ErrorBoundary>
```

- [ ] **Step 2: ChatMessageList 外层包裹**

两个 return（空态 div 与消息列表 div）各包一层 `<ErrorBoundary>`；实现为组件内两处 JSX 包裹，保持既有行为不变（空态本身不会抛错，但包住列表可防单个 Bubble 崩溃拖垮整页时仍有局部兜底）。

- [ ] **Step 3: 回归验证**

Run: `cd frontend && npx tsc --noEmit && npm run lint && npx vitest run`
Expected: 全绿（现有 177 测试 + 新增 2，共 179 左右）

Run: `cd backend && go build ./... && go test ./... -count=1`
Expected: 全绿（零后端改动）

- [ ] **Step 4: Commit**

```bash
git add frontend/src/main.tsx frontend/src/components/ChatMessageList.tsx
git commit -m "feat(frontend): mount error boundary at app root and chat area"
```
