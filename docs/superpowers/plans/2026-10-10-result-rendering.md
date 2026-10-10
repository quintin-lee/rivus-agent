# Result 智能渲染 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `RunDetail` 的 Result 从 raw JSON `<pre>` 升级为智能渲染：含 markdown 时排版为文本，其余保持美化 JSON 折叠，异常永不白屏。

**Architecture:** 新增纯函数 `detectResultView(result_json)` 做三态识别（markdown 字段/特征 → markdown；其余 → pretty-JSON；异常 → raw），`RunDetail` 内按识别结果分别渲染 `react-markdown`（GFM + sanitize）或现有 `<pre>`；原文 Source `<details>` 常驻。

**Tech Stack:** React 18, react-markdown ^9 + remark-gfm ^4 + rehype-sanitize ^6（XSS 保底，不执行 HTML/JS；不引入代码高亮库，代码块用现有 CSS）。

**Spec:** `docs/superpowers/specs/2026-10-10-realtime-experience-design.md` §3, §5, §6.

---

## File Structure

- Create: `frontend/src/lib/result-render.ts` — `detectResultView` 纯函数。
- Create: `frontend/src/lib/result-render.test.ts` — 三态 + 异常用例。
- Modify: `frontend/src/components/RunDetail.tsx:92-101` — Result 区块智能渲染 + 长结果限高滚动。
- Modify: `frontend/package.json` — 新增 3 依赖（`npm i`）。

后端零改动。

---

## Chunk 1: 识别函数 + 渲染

### Task 1: 依赖安装

**Files:**
- Modify: `frontend/package.json`

- [ ] **Step 1: 安装**

Run: `npm i -S react-markdown remark-gfm rehype-sanitize`（workdir: `frontend/`）
Expected: exit 0，`package.json` dependencies 新增三行（react-markdown ^9、remark-gfm ^4、rehype-sanitize ^6；若 npm 解析出 v10+/v5+/v7+ 且 peer 报 React 19 需求，必须降级到上述大版本——本仓库 React 18）。

- [ ] **Step 2: Commit**

```bash
git add frontend/package.json frontend/pnpm-lock.yaml
git commit -m "chore(result): add react-markdown gfm sanitize deps"
```

注意：仓库有 `.pnpm-store/`，实际包管理器可能是 pnpm。若 `package.json` 无 lockfile 变化，改用 `pnpm add react-markdown remark-gfm rehype-sanitize` 并提交对应 lockfile（先 `ls frontend | grep lock` 确认）。

### Task 2: `detectResultView`（先红后绿）

**Files:**
- Create: `frontend/src/lib/result-render.ts`
- Test: `frontend/src/lib/result-render.test.ts`

- [ ] **Step 1: 写 failing 测试**

```ts
import { describe, it, expect } from 'vitest'
import { detectResultView } from './result-render'

describe('detectResultView', () => {
  it('picks markdown field from object', () => {
    const r = detectResultView(JSON.stringify({ markdown: '# Hi\n\n- a' }))
    expect(r.kind).toBe('markdown')
    if (r.kind === 'markdown') expect(r.text).toBe('# Hi\n\n- a')
  })

  it('picks text/content/answer field in order', () => {
    expect(detectResultView(JSON.stringify({ text: 'hello' })).kind).toBe('markdown')
    expect(detectResultView(JSON.stringify({ content: 'hello' })).kind).toBe('markdown')
    expect(detectResultView(JSON.stringify({ answer: 'hello' })).kind).toBe('markdown')
  })

  it('detects markdown signals in plain string', () => {
    const r = detectResultView('"## Title\\n\\n[link](http://x)\\n\\n```js\\ncode\\n```"')
    expect(r.kind).toBe('markdown')
  })

  it('falls back to pretty json for plain objects', () => {
    const r = detectResultView(JSON.stringify({ a: 1 }))
    expect(r.kind).toBe('json')
    if (r.kind === 'json') expect(r.pretty).toBe(JSON.stringify({ a: 1 }, null, 2))
  })

  it('falls back to raw pre on invalid json', () => {
    const r = detectResultView('not json {{{')
    expect(r.kind).toBe('raw')
    if (r.kind === 'raw') expect(r.text).toBe('not json {{{')
  })

  it('falls back to raw pre on non-string field values', () => {
    const r = detectResultView(JSON.stringify({ markdown: 42 }))
    expect(r.kind).toBe('json')
  })
})
```

Run: `npx vitest run src/lib/result-render.test.ts`（workdir: `frontend/`）
Expected: FAIL（`detectResultView not defined` / 模块不存在）。

- [ ] **Step 2: 写最小实现**

```ts
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
```

识别顺序严格按 spec：对象字段（markdown/text/content/answer 任一字符串）→ 字符串 markdown 特征 → 美化 JSON → 原文。`formatJson`（RunDetail 内联）由本函数替代。

- [ ] **Step 3: 跑测试验证变绿**

Run: `npx vitest run src/lib/result-render.test.ts`（workdir: `frontend/`）
Expected: 6 用例 PASS。

- [ ] **Step 4: Commit**

```bash
git add frontend/src/lib/result-render.ts frontend/src/lib/result-render.test.ts
git commit -m "feat(result): detect markdown/json/raw view for run result"
```

### Task 3: `RunDetail` 接线渲染

**Files:**
- Modify: `frontend/src/components/RunDetail.tsx`

- [ ] **Step 1: 替换 Result 区块**

原 92-101 行 `<details>` 替换为：

```tsx
{activeRun.result_json && (
  <ResultViewBlock resultJson={activeRun.result_json} />
)}
```

文件内新增（放在 `RunDetail` 组件之后，替代原 `formatJson`——`formatJson` 整函数删除）：

```tsx
import { useMemo } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import rehypeSanitize from 'rehype-sanitize'
import { detectResultView } from '@/lib/result-render'

function ResultViewBlock({ resultJson }: { resultJson: string }) {
  const view = useMemo(() => {
    try {
      return detectResultView(resultJson)
    } catch {
      return { kind: 'raw', text: resultJson } as const
    }
  }, [resultJson])
  return (
    <details className="rounded-lg border border-border bg-card" open={view.kind === 'markdown'}>
      <summary className="cursor-pointer select-none px-3 py-2 text-xs font-medium text-muted-foreground hover:text-foreground">
        Result{view.kind === 'markdown' ? ' (markdown)' : ''}
      </summary>
      <div className="max-h-[400px] overflow-y-auto border-t border-border p-3">
        {view.kind === 'markdown' ? (
          <div className="prose-sm max-w-none text-sm text-foreground/90">
            <ReactMarkdown remarkPlugins={[remarkGfm]} rehypePlugins={[rehypeSanitize]}>
              {view.text}
            </ReactMarkdown>
          </div>
        ) : (
          <pre className="overflow-x-auto font-mono text-xs text-foreground/90">
            {view.kind === 'json' ? view.pretty : view.text}
          </pre>
        )}
        <details className="mt-2">
          <summary className="cursor-pointer select-none text-[11px] text-muted-foreground/70 hover:text-foreground">
            Source
          </summary>
          <pre className="mt-1 overflow-x-auto font-mono text-[11px] text-muted-foreground/70">{resultJson}</pre>
        </details>
      </div>
    </details>
  )
}
```

要点：markdown 态默认展开（`open`），其余折叠；长结果 `max-h-[400px]` 内部滚动；渲染异常回退原文永不白屏（`useMemo` 内 try/catch + `ReactMarkdown` 本身不抛——sanitize 保底 XSS）。`prose-sm` 需 `@tailwindcss/typography`——**不引入该插件**（YAGNI），`prose-sm` 类无定义则无样式但不报错；为避免静默无样式，改用裸 `text-sm` + `[&_pre]:` 等少量任意变体？保持最小：用 `text-sm text-foreground/90` 容器 + `space-y-2 [&_ul]:list-disc [&_ul]:pl-5 [&_ol]:list-decimal [&_ol]:pl-5 [&_pre]:overflow-x-auto [&_pre]:rounded [&_pre]:bg-muted [&_pre]:p-2 [&_pre]:font-mono [&_pre]:text-xs [&_a]:text-primary [&_a]:underline`。按此写，不依赖 typography 插件。

- [ ] **Step 2: 全量前端验证**

Run（workdir: `frontend/`）:
  1. `npx tsc --noEmit` → exit 0
  2. `npx vitest run` → 全部 PASS

- [ ] **Step 3: Commit**

```bash
git add frontend/src/components/RunDetail.tsx
git commit -m "feat(result): render markdown results with sanitized GFM"
```

---

## Acceptance（本 plan 完成标准）

- 含 markdown 的结果渲染为排版文本（含 GFM 表格/代码块/链接），纯 JSON 仍美化折叠 + Source 常驻。
- 非法 JSON / 渲染异常一律回退原文 `<pre>`，永不白屏。
- 前端 `tsc --noEmit` + `vitest run` 全绿。

Plan complete and saved to `docs/superpowers/plans/2026-10-10-result-rendering.md`. Ready to execute?（注：按你的要求全程不用 subagent，执行时用 executing-plans 当前会话批量执行。）
