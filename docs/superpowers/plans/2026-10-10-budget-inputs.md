# 预算表单 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `RunForm` 的 Advanced 折叠区追加 5 项预算数字输入，留空不传、填值透传，服务端 `ClampByServer` 权威收紧，后端零改动。

**Architecture:** 纯前端。新增 `assembleBudget` 纯函数（string 输入 → `Partial<Budget>`，空=omit），`RunForm` 加 5 个数字输入 + 即时越界提示（不拦截提交），`handleSubmit` 只带填了的值；`RunDetail` 预算行补 `max_output_bytes` 显示。

**Tech Stack:** React + 现有 shadcn `Input`，vitest + Testing Library。

**Spec:** `docs/superpowers/specs/2026-10-10-realtime-experience-design.md` §4, §5, §6.

**后端零改动**：`CreateRunReq.budget?: Partial<Budget>` 已支持（`frontend/src/lib/types.ts:126`），`createRunReq.Budget domain.Budget` JSON 缺字段即零值，`ClampByServer` 对 `want<=0` 取服务端上限（`backend/internal/domain/task.go:79-93`）——留空不传 ≡ 传 0 ≡ 服务端默认，行为一致。

---

## File Structure

- Create: `frontend/src/lib/budget.ts` — `assembleBudget` + 字段元数据（5 项 key/label）。
- Create: `frontend/src/lib/budget.test.ts` — 组装三态用例。
- Modify: `frontend/src/components/RunForm.tsx` — Advanced 区 5 输入 + 提示 + submit 透传。
- Modify: `frontend/src/components/RunDetail.tsx:104-110` — 预算行补 `max_output_bytes`。

---

## Chunk 1: 组装函数 + 表单 + 显示

### Task 1: `assembleBudget`（先红后绿）

**Files:**
- Create: `frontend/src/lib/budget.ts`
- Test: `frontend/src/lib/budget.test.ts`

- [ ] **Step 1: 写 failing 测试**

```ts
import { describe, it, expect } from 'vitest'
import { assembleBudget, BUDGET_FIELDS } from './budget'

describe('assembleBudget', () => {
  it('returns undefined when all blank', () => {
    expect(assembleBudget({})).toBeUndefined()
  })

  it('passes through filled positive ints only', () => {
    expect(
      assembleBudget({ max_model_calls: '10', max_tool_calls: '', max_iterations: 'abc' }),
    ).toEqual({ max_model_calls: 10 })
  })

  it('drops zero/negative/decimal values (blank=omit semantics)', () => {
    expect(assembleBudget({ max_duration_seconds: '0', max_output_bytes: '-5', max_iterations: '2.5' })).toBeUndefined()
  })

  it('exposes 5 fields', () => {
    expect(BUDGET_FIELDS.map((f) => f.key)).toEqual([
      'max_duration_seconds',
      'max_model_calls',
      'max_tool_calls',
      'max_iterations',
      'max_output_bytes',
    ])
  })
})
```

Run: `npx vitest run src/lib/budget.test.ts`（workdir: `frontend/`）
Expected: FAIL（模块不存在）。

- [ ] **Step 2: 写最小实现**

```ts
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
```

- [ ] **Step 3: 跑测试验证变绿**

Run: `npx vitest run src/lib/budget.test.ts`（workdir: `frontend/`）
Expected: 4 用例 PASS。

- [ ] **Step 4: Commit**

```bash
git add frontend/src/lib/budget.ts frontend/src/lib/budget.test.ts
git commit -m "feat(budget): assemble partial budget, blank means omit"
```

### Task 2: `RunForm` 接线

**Files:**
- Modify: `frontend/src/components/RunForm.tsx`

- [ ] **Step 1: 加 state + 输入区 + submit 透传**

  1. import 加：`import { assembleBudget, BUDGET_FIELDS, type BudgetKey } from '@/lib/budget'`。
  2. state 加：`const [budgetInput, setBudgetInput] = useState<Partial<Record<BudgetKey, string>>>({})`。
  3. Advanced 区（`{showAdvanced && (...)}` 内，Success criteria 块之后）追加：

```tsx
<div className="flex flex-col gap-1.5">
  <span className="text-xs text-muted-foreground/70">Budget（留空走服务端默认）</span>
  {BUDGET_FIELDS.map((f) => {
    const v = budgetInput[f.key] ?? ''
    const invalid = v.trim() !== '' && assembleBudget({ [f.key]: v }) === undefined
    return (
      <label key={f.key} className="flex items-center gap-2">
        <span className="w-28 shrink-0 text-xs text-foreground/80">{f.label}</span>
        <Input
          value={v}
          inputMode="numeric"
          placeholder="默认"
          className="h-8 text-xs"
          onChange={(e) =>
            setBudgetInput((prev) => ({ ...prev, [f.key]: e.target.value }))
          }
        />
        {invalid && (
          <span className="shrink-0 text-[11px] text-amber-500">需正整数，已忽略</span>
        )}
      </label>
    )
  })}
</div>
```

  4. `handleSubmit` 的 `req` 加一行：`budget: assembleBudget(budgetInput),`（undefined 字段 `JSON.stringify` 自动丢弃，等价不传）。
  5. 提交成功后重置加：`setBudgetInput({})`（与 `setGoal('')` 等并列）。

校验语义（spec §4）：即时提示但不拦截——`invalid` 只显示 amber 提示，`handleSubmit` 不检查；最终以服务端 `ClampByServer` 只收紧不放宽为准。

- [ ] **Step 2: 补 RunForm 提交载荷测试**

新建 `frontend/src/components/RunForm.test.tsx`（参考 `run-store.test.ts` 的 mock 风格 + `SessionPanel.test.tsx` 的组件渲染风格，先读后者 30 行确认 provider 包裹方式）：mock `@/lib/api` 的 `createRun`（`vi.fn().mockResolvedValue({run_id:'r1',...})`），mock store（`activeSessionId:'s1'`、`activeRun:null`、`activateRun: vi.fn()`），渲染后展开 Advanced、填 `max_model_calls=10`、点 Start Run，断言 `createRun` 首参 `budget` 为 `{max_model_calls:10}`；再测全留空时首参无 `budget` key（`expect('budget' in req).toBe(false)`——注意 `budget: undefined` 的 key 仍存在，改断言 `req.budget` 为 `undefined` 即可）。

先跑红（RunForm 无 budget 输入）→ 实现后跑绿。命令：`npx vitest run src/components/RunForm.test.tsx`。

- [ ] **Step 3: 全量前端验证**

Run（workdir: `frontend/`）:
  1. `npx tsc --noEmit` → exit 0
  2. `npx vitest run` → 全部 PASS

- [ ] **Step 4: Commit**

```bash
git add frontend/src/components/RunForm.tsx frontend/src/components/RunForm.test.tsx
git commit -m "feat(budget): advanced budget inputs with omit-when-blank"
```

### Task 3: `RunDetail` 预算行补全（收尾小步）

**Files:**
- Modify: `frontend/src/components/RunDetail.tsx:104-110`

- [ ] **Step 1: 加一行显示**

```tsx
<span>{budget.max_output_bytes} output bytes</span>
```

插到 `{budget.max_duration_seconds}s` 之后。`budget` 类型含该字段（`types.ts:61`），无类型风险。

- [ ] **Step 2: 验证 + Commit**

Run: `npx tsc --noEmit && npx vitest run`（workdir: `frontend/`）→ 全绿。

```bash
git add frontend/src/components/RunDetail.tsx
git commit -m "feat(budget): show max_output_bytes in run detail"
```

---

## Acceptance（本 plan 完成标准）

- 预算留空与之前行为一致（不传 ≡ 服务端默认）；填值后服务端收紧生效（`ClampByServer` 只收紧不放宽，后端零改动）。
- 越界/非法输入即时 amber 提示，不拦截提交。
- 前端 `tsc --noEmit` + `vitest run` 全绿。

Plan complete and saved to `docs/superpowers/plans/2026-10-10-budget-inputs.md`. Ready to execute?（注：按你的要求全程不用 subagent，执行时用 executing-plans 当前会话批量执行。）
