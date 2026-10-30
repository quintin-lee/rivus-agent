import { Check, Copy } from 'lucide-react'
import { t } from '@/i18n'
import { useCopy } from '@/lib/useCopy'

// CopyButton 复制指定文本到剪贴板，点击后短暂显示 ✓ 反馈。
export function CopyButton({ text, className = '' }: { text: string; className?: string }) {
  const { copied, copy } = useCopy()
  const label = copied ? t('common.copied') : t('common.copy')
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      className={
        'inline-flex h-5 w-5 items-center justify-center rounded text-muted-foreground transition-colors hover:text-foreground ' +
        className
      }
      onClick={() => copy(text)}
    >
      {copied ? <Check className="h-3 w-3 text-emerald-400" /> : <Copy className="h-3 w-3" />}
    </button>
  )
}
