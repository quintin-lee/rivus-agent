export type ResultView =
  | { kind: 'markdown'; text: string }
  | { kind: 'json'; pretty: string }
  | { kind: 'raw'; text: string }

const FIELD_ORDER = ['markdown', 'text', 'content', 'answer'] as const

const MD_SIGNALS: RegExp[] = [
  /^#{1,6}\s/m,        // 标题
  /^-{3,}$/m,          // 分隔线
  /^\s*[-*+]\s+\S/m,   // 无序列表
  /^\s*\d+\.\s+\S/m,   // 有序列表
  /```/,               // 代码块
  /\[[^\]]+\]\([^)]+\)/, // 链接
  /\*\*\S+\*\*/,       // 粗体
  /^\s*>\s+\S/m,       // 引用
]

export function looksLikeMarkdown(s: string): boolean {
  return MD_SIGNALS.some((re) => re.test(s))
}

export function detectResultView(resultJson: string): ResultView {
  let parsed: unknown
  try {
    parsed = JSON.parse(resultJson)
  } catch {
    return { kind: 'raw', text: resultJson }
  }
  if (typeof parsed === 'string') {
    return looksLikeMarkdown(parsed)
      ? { kind: 'markdown', text: parsed }
      : { kind: 'raw', text: parsed }
  }
  if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
    const obj = parsed as Record<string, unknown>
    for (const f of FIELD_ORDER) {
      if (typeof obj[f] === 'string') {
        return { kind: 'markdown', text: obj[f] as string }
      }
    }
  }
  try {
    return { kind: 'json', pretty: JSON.stringify(parsed, null, 2) }
  } catch {
    return { kind: 'raw', text: resultJson }
  }
}
