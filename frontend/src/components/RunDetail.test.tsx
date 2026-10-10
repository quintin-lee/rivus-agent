import { render, screen } from '@testing-library/react'
import { describe, it, expect } from 'vitest'
import { ResultViewBlock } from './RunDetail'

describe('ResultViewBlock', () => {
  it('renders markdown field as formatted text', () => {
    const { container } = render(
      <ResultViewBlock resultJson={JSON.stringify({ markdown: '# Hi\n\n| a | b |\n|---|---|\n| 1 | 2 |' })} />,
    )
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('Hi')
    expect(container.querySelector('table')).not.toBeNull()
  })

  it('sanitizes script tags instead of executing them', () => {
    const { container } = render(
      <ResultViewBlock resultJson={JSON.stringify({ text: 'hello <script>alert(1)</script>' })} />,
    )
    expect(container.querySelector('script')).toBeNull()
    expect(container.textContent).toContain('hello')
  })

  it('falls back to raw pre on invalid json without blanking', () => {
    render(<ResultViewBlock resultJson="not json {{{" />)
    expect(screen.getAllByText('not json {{{')).toHaveLength(2)
  })
})
