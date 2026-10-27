# Round-8 批2 前端流式打磨 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:executing-plans to implement this plan (user constraint: NO subagents). Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 补齐前端流式体验的两个真实缺口：流式输入指示器、终态失败 run 的「重新运行」（修复现有重试按钮对终态 run 调 resume 必 400 的 bug）。

**Architecture:** 纯前端改动。打字指示器内嵌 SystemStatusBubble（活跃 run 的实时载体，partialText 存在即模型正在产出）；重跑复用 run-store.submitRun 现有链路（pendingGoal → createRun → activateRun），从 run 对象回填参数，零新 API。

**Tech Stack:** React 18 + Zustand + Tailwind（内置 animate-bounce）+ vitest/@testing-library。**零新依赖**（spec 原判「react-markdown 唯一新增依赖」不成立：result-block.tsx 已用 react-markdown+remarkGfm+rehypeSanitize）。

**Spec 偏差（执行前核实，须回写 spec §批2）：**
1. Markdown 渲染**已存在**：`frontend/src/lib/result-block.tsx` L23 ReactMarkdown（sanitize 防 XSS），AssistantBubble 经 ResultViewBlock 渲染 result_json → 本批 0 工作量，跳过。
2. 重试按钮**已存在但语义错误**：`SystemStatusBubble.tsx:33-41` 对 failed/cancelled/timed_out（终态）调 `api.resumeRun` → 后端 RunService.Resume 对终态 run 报错。改为「重新运行」：`submitRun` 以原 run 的 session_id/goal/mode/constraints/success_criteria/budget 创建新 run。
3. 打字指示器落在 SystemStatusBubble（spec 原文「最新 assistant 消息尾部」不可行：assistant 消息仅终态后存在），触发条件用 `isActive && partialText`（token 实际到达）而非 sseStatus（连接状态≠模型输出）。

---

### Task 1: 打字指示器（SystemStatusBubble 动画三点）

**Files:**
- Modify: `frontend/src/components/Messages/SystemStatusBubble.tsx:101-105`
- Test: `frontend/src/components/Messages/SystemStatusBubble.test.tsx`

- [ ] **Step 1: 写失败测试**（追加到现有 describe）

```tsx
it('shows a typing indicator while streaming partial text on an active run', () => {
  render(<SystemStatusBubble run={run('running')} partialText="正在分析…" onRetryRun={vi.fn()} />)
  expect(screen.getByLabelText('正在输入')).toBeInTheDocument()
})

it('hides the typing indicator without partial text or on a terminal run', () => {
  const { rerender } = render(<SystemStatusBubble run={run('running')} onRetryRun={vi.fn()} />)
  expect(screen.queryByLabelText('正在输入')).not.toBeInTheDocument()
  rerender(<SystemStatusBubble run={run('succeeded')} partialText="x" onRetryRun={vi.fn()} />)
  expect(screen.queryByLabelText('正在输入')).not.toBeInTheDocument()
})
```

- [ ] **Step 2: 运行确认失败**

Run: `cd frontend && npx vitest run src/components/Messages/SystemStatusBubble.test.tsx`
Expected: FAIL（找不到「正在输入」元素）

- [ ] **Step 3: 实现**（替换 L101-105 的 partialText 块）

```tsx
{isActive && partialText && (
  <>
    <p className="mt-1 max-h-40 overflow-hidden text-xs whitespace-pre-wrap text-foreground/80">
      {partialText}
    </p>
    <span className="mt-1 inline-flex items-center gap-1" aria-label="正在输入">
      {[0, 150, 300].map((d) => (
        <span
          key={d}
          className="h-1.5 w-1.5 animate-bounce rounded-full bg-muted-foreground"
          style={{ animationDelay: `${d}ms` }}
        />
      ))}
    </span>
  </>
)}
```

- [ ] **Step 4: 运行确认通过**

Run: `cd frontend && npx vitest run src/components/Messages/SystemStatusBubble.test.tsx`
Expected: PASS（新旧全部）

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/Messages/SystemStatusBubble.tsx frontend/src/components/Messages/SystemStatusBubble.test.tsx
git commit -m "feat(frontend): animated typing indicator while model tokens stream"
```

### Task 2: 重新运行按钮（修复终态 resume 必败）

**Files:**
- Modify: `frontend/src/components/Messages/SystemStatusBubble.tsx`（删 handleRetry/resumeRun，Retry→onRerunRun）
- Modify: `frontend/src/components/ChatMessageList.tsx:37-39,71-80`（handleRerun + 传参）
- Test: `frontend/src/components/Messages/SystemStatusBubble.test.tsx`、`frontend/src/components/ChatMessageList.test.tsx`

- [ ] **Step 1: 更新/新增失败测试**

SystemStatusBubble.test.tsx：改写 L46-53 断言（重试不再调 api.resumeRun，改调 onRerunRun(run)）；新增 succeeded 无按钮：

```tsx
it('re-runs a failed run via onRerunRun instead of resumeRun', async () => {
  const onRerunRun = vi.fn()
  const user = userEvent.setup()
  render(<SystemStatusBubble run={run('failed')} onRetryRun={vi.fn()} onRerunRun={onRerunRun} />)
  await user.click(screen.getByRole('button', { name: '重新运行' }))
  expect(api.resumeRun).not.toHaveBeenCalled()
  expect(onRerunRun).toHaveBeenCalledWith(expect.objectContaining({ id: 'r1', goal: expect.any(String) }))
})

it('shows no re-run button on a succeeded run', () => {
  render(<SystemStatusBubble run={run('succeeded')} onRetryRun={vi.fn()} onRerunRun={vi.fn()} />)
  expect(screen.queryByRole('button', { name: '重新运行' })).not.toBeInTheDocument()
})
```

ChatMessageList.test.tsx：新增 wiring 测试（失败 run 点击 → submitRun 收到映射参数；run-store mock 需含 submitRun，参照该文件现有 store mock 模式）：

```tsx
it('wires re-run to submitRun with the original run parameters', async () => {
  // render list with a failed run in runHistory; click 重新运行
  // expect useRunStore.getState().submitRun 被调（或 mock）:
  // { sessionId: 's1', goal, mode, constraints, successCriteria, budget }
})
```

- [ ] **Step 2: 运行确认失败**

Run: `cd frontend && npx vitest run src/components/Messages/ src/components/ChatMessageList.test.tsx`
Expected: FAIL（onRerunRun prop 不存在 / 仍调 resumeRun）

- [ ] **Step 3: 实现**

SystemStatusBubble.tsx：
- Props 增 `onRerunRun: (run: Run) => void`
- 删 `handleRetry`（resumeRun）与 api.resumeRun 调用
- isFailed 按钮 L88-96：文案 `重新运行`，`onClick={() => onRerunRun(run)}`

ChatMessageList.tsx：

```tsx
const submitRun = useRunStore((s) => s.submitRun)
const handleRerun = (run: Run) => {
  void submitRun({
    sessionId: run.session_id,
    goal: run.goal,
    mode: run.mode === 'plan_execute' ? 'plan_execute' : 'react',
    constraints: run.constraints,
    successCriteria: run.success_criteria,
    budget: run.budget,
  })
}
// case 'system-status': <SystemStatusBubble ... onRerunRun={handleRerun} />
```

（`import type { Run } from '@/lib/types'` 按需补；mode 用等值窄化避免类型断言。）

- [ ] **Step 4: 运行确认通过 + tsc**

Run: `cd frontend && npx vitest run && npx tsc --noEmit`
Expected: 全 PASS（141+ 新增）、tsc 0 错误

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/Messages/SystemStatusBubble.tsx frontend/src/components/Messages/SystemStatusBubble.test.tsx frontend/src/components/ChatMessageList.tsx frontend/src/components/ChatMessageList.test.tsx
git commit -m "fix(frontend): terminal-failed runs re-run instead of resume (400); wire submitRun mapping"
```

### Task 3: spec 偏差回写 + 文档 + 批2 验收

**Files:**
- Modify: `docs/superpowers/specs/2026-10-27-structure-frontend-mcp-design.md`（§批2 偏差注记）
- Modify: `frontend/README.md` / `frontend/README.en.md`（components 行 + 打字指示器/重新运行）

- [ ] **Step 1: spec §批2 尾部追加偏差段**（markdown 已存在 → 0 工作量；重试语义 resume→重新运行 + 原因；指示器落点与触发条件）
- [ ] **Step 2: README zh/en components 行追加「流式打字指示器、终态失败重新运行」**
- [ ] **Step 3: 批2 验收** — `cd frontend && npx tsc --noEmit`（0）+ `npx vitest run`（≥147 全过）；`cd backend && go build ./... && go test ./internal/... -count=1`（后端零改动回归）
- [ ] **Step 4: Commit**

```bash
git add docs/superpowers/specs/2026-10-27-structure-frontend-mcp-design.md frontend/README.md frontend/README.en.md
git commit -m "docs(round8-batch2): record scope deviations (markdown pre-existing, rerun semantics) + readme"
```

---

## 批2 完成定义
- 新增/更新测试 ≥6（打字 2 + 重跑 2 + wiring 1 + 既有 resume 断言改写 1）
- 全量 vitest 绿 + tsc 0 错误；后端零行为改动
- spec 偏差已回写；3 commits（每 Task 一个）
