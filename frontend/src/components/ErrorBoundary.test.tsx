import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { ErrorBoundary } from './ErrorBoundary'

function Thrower({ active }: { active: boolean }) {
  if (active) throw new Error('boom')
  return <div>child-content</div>
}

describe('ErrorBoundary', () => {
  let consoleSpy: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    consoleSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
  })

  afterEach(() => {
    consoleSpy.mockRestore()
  })

  it('renders fallback when child throws, recovers on retry', () => {
    let throwing = true
    const { rerender, getByText, queryByText } = render(
      <ErrorBoundary>
        <Thrower active={throwing} />
      </ErrorBoundary>,
    )
    expect(getByText('页面出错了，请重试')).toBeTruthy()
    expect(getByText('重试')).toBeTruthy()

    // 先换成不抛错的子树（兜底期间 children 不渲染，不会抛），再点重试恢复。
    throwing = false
    rerender(
      <ErrorBoundary>
        <Thrower active={throwing} />
      </ErrorBoundary>,
    )
    expect(queryByText('child-content')).toBeNull()
    fireEvent.click(getByText('重试'))
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
