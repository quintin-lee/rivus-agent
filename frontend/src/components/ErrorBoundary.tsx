import React from 'react'
import { AlertTriangle } from 'lucide-react'
import { t } from '@/i18n'

interface Props {
  children: React.ReactNode
}

interface State {
  error: Error | null
}

/** 渲染期兜底：子树抛错时显示可重试的占位 UI（class 组件是 React 错误边界唯一形式）。 */
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
