import { useCallback, useEffect, useRef, useState } from 'react'

// useCopy 提供"复制 + 短暂已复制反馈"的小状态机；clipboard 不可用时 copy 为 no-op。
export function useCopy(resetMs = 1500) {
  const [copied, setCopied] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => () => {
    if (timer.current) clearTimeout(timer.current)
  }, [])

  const copy = useCallback(
    (text: string) => {
      if (!navigator.clipboard?.writeText) return
      void navigator.clipboard.writeText(text).then(() => {
        setCopied(true)
        if (timer.current) clearTimeout(timer.current)
        timer.current = setTimeout(() => setCopied(false), resetMs)
      })
    },
    [resetMs],
  )

  return { copied, copy }
}
