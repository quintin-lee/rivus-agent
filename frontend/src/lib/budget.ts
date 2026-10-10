import type { Budget } from './types'

export type BudgetKey = keyof Budget

export interface BudgetField {
  key: BudgetKey
  label: string
  hint: string
}

export const BUDGET_FIELDS: BudgetField[] = [
  { key: 'max_duration_seconds', label: '时长上限（秒）', hint: '正整数' },
  { key: 'max_model_calls', label: '模型调用数', hint: '正整数' },
  { key: 'max_tool_calls', label: '工具调用数', hint: '正整数' },
  { key: 'max_iterations', label: '迭代次数', hint: '正整数' },
  { key: 'max_output_bytes', label: '输出字节上限', hint: '正整数' },
]

/** 表单 string 值 → Partial<Budget>；空/非法/非正整数一律 omit（走服务端默认）。 */
export function assembleBudget(input: Partial<Record<BudgetKey, string>>): Partial<Budget> | undefined {
  const out: Partial<Budget> = {}
  for (const f of BUDGET_FIELDS) {
    const raw = (input[f.key] ?? '').trim()
    if (!raw) continue
    if (!/^\d+$/.test(raw)) continue
    const n = parseInt(raw, 10)
    if (n <= 0) continue
    out[f.key] = n
  }
  return Object.keys(out).length ? out : undefined
}
