# Round 32: Frontend i18n Full Coverage + Interaction Robustness — Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate every hardcoded user-visible string in the frontend (zh/en dictionary coverage), make session operation failures visible, fix keyboard/touch accessibility of SessionList icon buttons, and complete the run-history status filter.

**Architecture:** Three independently committable batches in `frontend/src` only. Batch 1 is mechanical string→`t()` migration (component namespaces + `lib/budget.ts` label keys + `eventSummary` switch → `timeline.*` lookup). Batch 2 adds a `lastError` field to `session-store` rendered as an inline error row in SessionList (no toast infra), `focus-visible` + `aria-label` on hover-only buttons, and 4 missing filter chips. Batch 3 deletes dead code and updates CHANGELOG.

**Tech Stack:** React 18 + zustand + TypeScript (strict, `noUnusedLocals`), vitest + @testing-library/react, Tailwind utility classes, existing `src/i18n/index.ts` (`t(key, params)` with `{param}` interpolation and zh fallback).

**Spec:** `docs/superpowers/specs/2026-11-05-round32-frontend-i18n-robustness-design.md`

**Verification gates (every batch):** `npx tsc --noEmit` (0 errors) + `npx vitest run` (all green). `npm run lint` is broken repo-wide (ESLint 10 needs flat config; repo has legacy `.eslintrc.cjs`) — pre-existing, NOT a gate. All frontend commands run from `frontend/`.

---

## File Structure

| File | Action | Responsibility |
|------|--------|----------------|
| `frontend/src/i18n/index.ts` | Modify | Add ~60 keys ×2 languages in component namespaces (`newrun.*`, `settings.*`, `approval.*`, `bubble.*`, `input.*`, `events.*`, `subagent.*`, `budget.*`, `sessionlist.*`, `status.budgetUsage` ext) |
| `frontend/src/lib/budget.ts` | Modify | `BUDGET_FIELDS` label/hint → i18n key names |
| `frontend/src/lib/status.ts` | Modify | `eventSummary` English branches → `timeline.*` lookup |
| `frontend/src/components/NewRunDialog.tsx` | Modify | All strings → `t('newrun.*')` / `t('budget.*')` |
| `frontend/src/components/SettingsDialog.tsx` | Modify | zh strings → `t('settings.*')` |
| `frontend/src/components/ApprovalCard.tsx` | Modify | English strings → `t('approval.*')` |
| `frontend/src/components/Messages/AssistantBubble.tsx` | Modify | English → `t('bubble.*')` |
| `frontend/src/components/Messages/UserBubble.tsx` | Modify | English → `t('bubble.*')` |
| `frontend/src/components/Messages/ChatInputBar.tsx` | Modify | English → `t('input.*')` |
| `frontend/src/components/Messages/SystemEventsBubble.tsx` | Modify | English → `t('events.*')` |
| `frontend/src/components/Messages/SystemStatusBubble.tsx` | Modify | Raw status badge → `status.*`; zh usage line → `status.budgetUsage` w/ seconds |
| `frontend/src/components/Messages/SubagentCard.tsx` | Modify | zh literals → existing `common.*` + new `subagent.*` keys |
| `frontend/src/components/SessionList.tsx` | Modify | Badge → `status.*`; buttons a11y; inline error row |
| `frontend/src/components/RunsHistoryDialog.tsx` | Modify | Filter chips 4 → 8 states |
| `frontend/src/store/session-store.ts` | Modify | Add `lastError`/`clearError`; try/catch on 5 actions |
| `frontend/src/lib/status.test.ts` | Modify | Add eventSummary parity test |
| `frontend/src/lib/budget.test.ts` | Modify | Assert label/hint are i18n keys |
| `frontend/src/store/session-store.test.ts` | Create | lastError behavior |
| `frontend/src/components/SessionList.test.tsx` | Modify | Error row + a11y tests |
| `frontend/src/components/RunsHistoryDialog.test.tsx` | Modify | Filter chips test |
| `frontend/src/components/Messages/index.ts` | Delete | Dead barrel (zero importers, verified) |
| `frontend/src/components/ui/skeleton.tsx` | Delete (conditional) | Only if grep confirms zero importers at execution time |
| `CHANGELOG.md` | Modify | R32 entry |

Import rule preserved: `eventSummary` already imports `t` in `lib/status.ts` (`import { t } from '@/i18n'`, line 4) — no new dependency cycles.

---

## Chunk 1: i18n Migration (Batch 1)

### Task 1: New i18n keys (zh + en dictionaries)

**Files:**
- Modify: `frontend/src/i18n/index.ts` (zh block after line ~99, en block after line ~244 — insert near related keys)

- [ ] **Step 1: Extend `status.budgetUsage` to accept optional seconds**

Current (zh line 99): `'status.budgetUsage': '用量 工具 {tool}/模型 {model}'`
en (line 244): `'status.budgetUsage': 'Usage tool {tool}/model {model}'`

Change to (both languages; `t()` splits on exact `{name}` tokens, so a missing `seconds` param leaves the literal suffix — handled by call sites, see Task 9):

zh: `'status.budgetUsage': '用量 工具 {tool}/模型 {model}'` — unchanged base. The live usage line in SystemStatusBubble needs denominators + seconds, which this template lacks — that call site uses a new variant key `status.budgetUsageMax` (added to this Task's dicts, consumed in Task 4 Step 5). **Do not add `{seconds}` to the base template.** Rationale: `RunDetailDialog.tsx:179-183` calls without `seconds`; template interpolation of an absent param would leave a literal `{seconds}` fragment.

- [ ] **Step 2: Add zh keys** (single insertion block, after `'status.budgetUpdated'` line 100):

```ts
  // R32 — status usage variants
  'status.budgetUsageMax': '用量 工具 {tool}/{toolMax} · 模型 {model}/{modelMax} · {seconds}s',
  // R32 — NewRunDialog
  'newrun.description': '为本次会话的下一次运行配置高级选项。',
  'newrun.goal': '目标',
  'newrun.goalPlaceholder': '描述目标…',
  'newrun.mode': '模式',
  'newrun.constraints': '约束条件',
  'newrun.constraintsPlaceholder': '添加一条约束（如：不要删除文件）',
  'newrun.successCriteria': '成功标准',
  'newrun.successCriteriaPlaceholder': '添加一条成功标准',
  'newrun.budget': '预算（可选）',
  'newrun.cancel': '取消',
  'newrun.create': '创建运行',
  // R32 — lib/budget.ts labels
  'budget.max_duration_seconds': '时长上限（秒）',
  'budget.max_model_calls': '模型调用数',
  'budget.max_tool_calls': '工具调用数',
  'budget.max_iterations': '迭代次数',
  'budget.max_output_bytes': '输出字节上限',
  'budget.hint': '正整数',
  // R32 — SettingsDialog
  'settings.title': '模型设置',
  'settings.openTitle': '模型设置',
  'settings.description': '全局默认模型，新提交的 Run 立即生效；在途 Run 不受影响。',
  'settings.provider': '供应商',
  'settings.model': '模型',
  'settings.apiKeySet': '已设置',
  'settings.apiKeyUnset': '未设置',
  'settings.apiKeyPlaceholder': '留空表示不修改',
  'settings.saved': '已保存，新提交的 Run 生效',
  // R32 — ApprovalCard
  'approval.badgePending': '待审批',
  'approval.badgeApproved': '已批准',
  'approval.badgeRejected': '已拒绝',
  'approval.badgeExpired': '已过期',
  'approval.approvedLine': '✓ 已批准 {tool}',
  'approval.rejectedLine': '✗ 已拒绝 {tool}',
  'approval.expiredLine': '审批已过期 · {tool}',
  'approval.title': '工具审批',
  'approval.approve': '批准',
  'approval.reject': '拒绝',
  'approval.rejectTitle': '拒绝审批',
  'approval.reasonPlaceholder': '原因（可选）',
  'approval.cancel': '取消',
  'approval.confirmReject': '确认拒绝',
  // R32 — AssistantBubble / UserBubble
  'bubble.cancelledPartial': '已取消 — 部分结果',
  'bubble.runCancelled': '运行已取消',
  'bubble.runTimedOut': '运行已超时',
  'bubble.runFailed': '运行失败',
  'bubble.result': '结果',
  'bubble.sending': '发送中…',
  'bubble.failed': '失败：{msg}',
  'bubble.unknownError': '未知错误',
  // R32 — ChatInputBar
  'input.placeholder': '描述本次会话的任务…',
  'input.stop': '⏹ 停止',
  'input.stopping': '停止中…',
  'input.send': '发送',
  'input.advancedSettings': '高级设置',
  // R32 — mode display names (NewRunDialog mode buttons)
  'newrun.modeReact': 'ReAct',
  'newrun.modePlanExecute': 'Plan & Execute',
  // R32 — SystemEventsBubble
  'events.count': '{n} 条事件',
  'events.masked': '[已脱敏]',
  'events.hide': '收起',
  'events.raw': '原始',
  // R32 — SubagentCard
  'subagent.empty': '（无子 run 记录）',
  // R32 — SessionList
  'sessionlist.actionFailed': '操作失败',
  'sessionlist.close': '关闭',
```

- [ ] **Step 3: Add en keys** (mirror block, after `'status.budgetUpdated'` en line ~245):

```ts
  // R32 — status usage variants
  'status.budgetUsageMax': 'Usage tool {tool}/{toolMax} · model {model}/{modelMax} · {seconds}s',
  // R32 — NewRunDialog
  'newrun.title': 'New Run',
  'newrun.description': 'Advanced options for the next run in this session.',
  'newrun.goal': 'Goal',
  'newrun.goalPlaceholder': 'Describe the goal…',
  'newrun.mode': 'Mode',
  'newrun.constraints': 'Constraints',
  'newrun.constraintsPlaceholder': 'Add a constraint (e.g. do not delete files)',
  'newrun.successCriteria': 'Success criteria',
  'newrun.successCriteriaPlaceholder': 'Add a success criterion',
  'newrun.budget': 'Budget (optional)',
  'newrun.cancel': 'Cancel',
  'newrun.create': 'Create run',
  // R32 — lib/budget.ts labels
  'budget.max_duration_seconds': 'Max duration (s)',
  'budget.max_model_calls': 'Max model calls',
  'budget.max_tool_calls': 'Max tool calls',
  'budget.max_iterations': 'Max iterations',
  'budget.max_output_bytes': 'Max output bytes',
  'budget.hint': 'positive integer',
  // R32 — SettingsDialog
  'settings.title': 'Model Settings',
  'settings.openTitle': 'Model settings',
  'settings.description': 'Global default model. Applies to newly submitted runs; in-flight runs unaffected.',
  'settings.provider': 'Provider',
  'settings.model': 'Model',
  'settings.apiKeySet': 'Set',
  'settings.apiKeyUnset': 'Not set',
  'settings.apiKeyPlaceholder': 'Leave blank to keep current',
  'settings.saved': 'Saved. Applies to newly submitted runs.',
  // R32 — ApprovalCard
  'approval.badgePending': 'Pending',
  'approval.badgeApproved': 'Approved',
  'approval.badgeRejected': 'Rejected',
  'approval.badgeExpired': 'Expired',
  'approval.approvedLine': '✓ Approved {tool}',
  'approval.rejectedLine': '✗ Rejected {tool}',
  'approval.expiredLine': 'Approval expired · {tool}',
  'approval.title': 'Tool approval',
  'approval.approve': 'Approve',
  'approval.reject': 'Reject',
  'approval.rejectTitle': 'Reject approval',
  'approval.reasonPlaceholder': 'Reason (optional)',
  'approval.cancel': 'Cancel',
  'approval.confirmReject': 'Confirm reject',
  // R32 — AssistantBubble / UserBubble
  'bubble.cancelledPartial': 'Cancelled — partial result',
  'bubble.runCancelled': 'Run cancelled',
  'bubble.runTimedOut': 'Run timed out',
  'bubble.runFailed': 'Run failed',
  'bubble.result': 'Result',
  'bubble.sending': 'Sending…',
  'bubble.failed': 'Failed: {msg}',
  'bubble.unknownError': 'unknown error',
  // R32 — ChatInputBar
  'input.placeholder': 'Describe the task for this session…',
  'input.stop': '⏹ Stop',
  'input.stopping': 'Stopping…',
  'input.send': 'Send',
  'input.advancedSettings': 'Advanced settings',
  // R32 — mode display names (NewRunDialog mode buttons)
  'newrun.modeReact': 'ReAct',
  'newrun.modePlanExecute': 'Plan & Execute',
  // R32 — SystemEventsBubble
  'events.count': '{n} events',
  'events.masked': '[masked]',
  'events.hide': 'hide',
  'events.raw': 'raw',
  // R32 — SubagentCard
  'subagent.empty': '(no child runs)',
  // R32 — SessionList
  'sessionlist.actionFailed': 'Action failed',
  'sessionlist.close': 'Close',
```

- [ ] **Step 4: Verify no key parity break**

Run: `npx vitest run src/i18n` — existing i18n tests (if any) pass; then `npx tsc --noEmit`.
Expected: PASS. (zh/en key parity is asserted by dict structure, not runtime.)

- [ ] **Step 5: Commit**

```bash
git add src/i18n/index.ts
git commit -m "feat(frontend): add R32 i18n keys — newrun/settings/approval/bubble/input/events/subagent/budget/sessionlist"
```

### Task 2: NewRunDialog + lib/budget.ts migration

**Files:**
- Modify: `frontend/src/lib/budget.ts:11-17`, `frontend/src/components/NewRunDialog.tsx:47-147`, test `frontend/src/lib/budget.test.ts`

- [ ] **Step 1: Write failing test for budget i18n keys**

`src/lib/budget.test.ts` already imports `BUDGET_FIELDS` (line 2, alongside `assembleBudget`) — extend that import, do NOT re-import. Add `import { t } from '@/i18n'` at top, then append:

```ts
describe('budget field i18n', () => {
  it('labels and hints are i18n keys resolvable in both languages', () => {
    for (const f of BUDGET_FIELDS) {
      expect(t(f.label)).not.toBe(f.label) // key resolves, not passthrough
      expect(t(f.hint)).not.toBe(f.hint)
    }
  })
})
```

Note: `BUDGET_FIELDS` currently holds raw zh text (`时长上限（秒）`), which IS resolvable as a dict *value* lookup miss → returns key itself = the Chinese text; the test asserts migration to keys happened (`t(key) !== key`).

- [ ] **Step 2: Run test, verify failure**

Run: `npx vitest run src/lib/budget.test.ts`
Expected: FAIL — `t('时长上限（秒）')` returns the raw string (no such key), so `expect(...).not.toBe(...)` fails.

- [ ] **Step 3: Migrate `lib/budget.ts`**

```ts
export const BUDGET_FIELDS: BudgetField[] = [
  { key: 'max_duration_seconds', label: 'budget.max_duration_seconds', hint: 'budget.hint' },
  { key: 'max_model_calls', label: 'budget.max_model_calls', hint: 'budget.hint' },
  { key: 'max_tool_calls', label: 'budget.max_tool_calls', hint: 'budget.hint' },
  { key: 'max_iterations', label: 'budget.max_iterations', hint: 'budget.hint' },
  { key: 'max_output_bytes', label: 'budget.max_output_bytes', hint: 'budget.hint' },
]
```

Comment above array becomes: `/** label/hint 为 i18n key（budget.*），渲染层用 t() 解析。 */`

- [ ] **Step 4: Migrate NewRunDialog strings**

In `src/components/NewRunDialog.tsx`: add `import { t } from '@/i18n'`. Replace:
- Line 52 `<DialogTitle>New Run</DialogTitle>` → `t('newrun.title')`
- Line 54-56 description → `t('newrun.description')`
- Line 60 `Goal` → `t('newrun.goal')`; line 63 placeholder → `t('newrun.goalPlaceholder')`
- Line 68 `Mode` → `t('newrun.mode')`
- Line 86 mode button label: `{m === 'react' ? 'ReAct' : 'Plan & Execute'}` → `{t(m === 'react' ? 'newrun.modeReact' : 'newrun.modePlanExecute')}`
- Line 74/77 `ConstraintEditor label=` → `t('newrun.constraints')` / `t('newrun.successCriteria')`; placeholders lines 75/78 → `t('newrun.constraintsPlaceholder')` / `t('newrun.successCriteriaPlaceholder')`
- Line 82 `Budget (optional)` → `t('newrun.budget')`
- Line 86 `{f.label} <span ...>{f.hint}</span>` → `{t(f.label)} <span className="opacity-60">{t(f.hint)}</span>`
- Line 141 `Cancel` → `t('newrun.cancel')`; line 147 `Create run` → `t('newrun.create')`

- [ ] **Step 5: Run tests, verify pass**

Run: `npx vitest run src/lib/budget.test.ts src/components/NewRunDialog.test.tsx && npx tsc --noEmit`
Expected: PASS. (NewRunDialog.test.tsx existing assertions may reference English labels — update them to use `t()`-resolved zh strings or render with lang=en; prefer asserting `t('newrun.title')` output.)

- [ ] **Step 6: Commit**

```bash
git add src/lib/budget.ts src/lib/budget.test.ts src/components/NewRunDialog.tsx src/components/NewRunDialog.test.tsx
git commit -m "feat(frontend): NewRunDialog + budget labels i18n"
```

### Task 3: SettingsDialog + ApprovalCard migration

**Files:**
- Modify: `frontend/src/components/SettingsDialog.tsx:76,82,84,89,102,115,123,129`, `frontend/src/components/ApprovalCard.tsx:28-32,44-49,54-97`

- [ ] **Step 1: Migrate SettingsDialog** (add `import { t } from '@/i18n'`):
- `:76` `title="模型设置"` → `title={t('settings.openTitle')}`
- `:82` `<DialogTitle>模型设置</DialogTitle>` → `{t('settings.title')}`
- `:84` DialogDescription `全局默认模型，新提交的 Run 立即生效；在途 Run 不受影响。` → `{t('settings.description')}` (key added in Task 1)
- `:89` `供应商` → `{t('settings.provider')}`
- `:102` `模型` → `{t('settings.model')}`
- `:115` `{settings.api_key_set ? '已设置' : '未设置'}` → `{settings.api_key_set ? t('settings.apiKeySet') : t('settings.apiKeyUnset')}`
- `:123` `placeholder="留空表示不修改"` → `placeholder={t('settings.apiKeyPlaceholder')}`
- `:129` `已保存，新提交的 Run 生效` → `{t('settings.saved')}`

- [ ] **Step 2: Migrate ApprovalCard** (add `import { t } from '@/i18n'`). `BADGE` is a module-level const using English labels — convert to functions of `t()`:

```ts
const BADGE_CLS: Record<Props['state'], string> = {
  pending: 'bg-amber-500/15 text-amber-600 dark:text-amber-400',
  approved: 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400',
  rejected: 'bg-rose-500/15 text-rose-600 dark:text-rose-400',
  expired: 'bg-muted text-muted-foreground',
}
const BADGE_KEY: Record<Props['state'], string> = {
  pending: 'approval.badgePending',
  approved: 'approval.badgeApproved',
  rejected: 'approval.badgeRejected',
  expired: 'approval.badgeExpired',
}
```

Resolved line (lines 44-49):
```ts
const line =
  state === 'approved'
    ? t('approval.approvedLine', { tool: toolName })
    : state === 'rejected'
      ? t('approval.rejectedLine', { tool: toolName })
      : t('approval.expiredLine', { tool: toolName })
```
Then `:59` badge label → `{t(BADGE_KEY[state])}` with className `{BADGE_CLS[state]}`; `:63` `Approve` → `{t('approval.approve')}`; `:67` `Reject` → `{t('approval.reject')}`; `:79` `<DialogTitle>Reject approval` → `{t('approval.rejectTitle')}`; `:84` placeholder → `{t('approval.reasonPlaceholder')}`; `:88` `Cancel` → `{t('approval.cancel')}`; `:93` confirm button → `{t('approval.confirmReject')}` (verify exact current text — likely `Confirm`; update key if needed).

- [ ] **Step 3: Update affected tests**

`ApprovalCard.test.tsx` and `SettingsDialog.test.tsx` may assert English/zh literals — update to `t()`-resolved expectations (default test lang zh unless test sets otherwise).

Run: `npx vitest run src/components/ApprovalCard.test.tsx src/components/SettingsDialog.test.tsx && npx tsc --noEmit`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add src/components/SettingsDialog.tsx src/components/ApprovalCard.tsx src/components/SettingsDialog.test.tsx src/components/ApprovalCard.test.tsx src/i18n/index.ts
git commit -m "feat(frontend): SettingsDialog + ApprovalCard i18n"
```

### Task 4: Message bubbles + ChatInputBar migration

**Files:**
- Modify: `frontend/src/components/Messages/AssistantBubble.tsx`, `UserBubble.tsx`, `SystemEventsBubble.tsx`, `SystemStatusBubble.tsx`, `ChatInputBar.tsx`, `SubagentCard.tsx`

- [ ] **Step 1: AssistantBubble** (add `import { t } from '@/i18n'`):
- `:10` `Cancelled — partial result` → `{t('bubble.cancelledPartial')}`
- `:18` `Run cancelled` → `{t('bubble.runCancelled')}`
- `:26` `Run timed out` → `{t('bubble.runTimedOut')}`
- `:33` `Run failed` → `{t('bubble.runFailed')}`
- `:41` `Result` → `{t('bubble.result')}`

- [ ] **Step 2: UserBubble**:
- `:20` `Sending…` → `{t('bubble.sending')}`
- `:24` `Failed: {message.error ?? 'unknown error'}` → `{t('bubble.failed', { msg: message.error ?? t('bubble.unknownError') })}`

- [ ] **Step 3: ChatInputBar**:
- `:45` placeholder → `{t('input.placeholder')}`
- `:58-59` `title="Advanced settings"` / `aria-label="Advanced settings"` (⚙ button) → `title={t('input.advancedSettings')}` / `aria-label={t('input.advancedSettings')}`
- `:72` `{cancelling ? 'Stopping…' : '⏹ Stop'}` → `{cancelling ? t('input.stopping') : t('input.stop')}`
- `:81` `Send` → `{t('input.send')}`

- [ ] **Step 4: SystemEventsBubble**:
- `:21` `{message.events.length} events` → `{t('events.count', { n: message.events.length })}`
- `:52` `'[masked]'` → `t('events.masked')`
- `:60` `{showRaw ? 'hide' : 'raw'}` → `{showRaw ? t('events.hide') : t('events.raw')}`

- [ ] **Step 5: SystemStatusBubble**:
- `:40` `{run.status.replace('_', ' ')}` → `{t(`status.${run.status}`)}` (backend `RunStatus` values all have `status.*` keys; RunStatus is a closed union type — `t()` passthrough impossible)
- `:109-112` usage line — current template renders denominators from `run.budget` (`工具 {progress.toolCalls}/{run.budget.max_tool_calls} · 模型 {progress.modelCalls}/{run.budget.max_model_calls} · {seconds}s`). `status.budgetUsage` has no denominator slots, so reuse it via a **new variant key** `status.budgetUsageMax` (added to Task 1 dicts):
  - zh: `'status.budgetUsageMax': '用量 工具 {tool}/{toolMax} · 模型 {model}/{modelMax} · {seconds}s'`
  - en: `'status.budgetUsageMax': 'Usage tool {tool}/{toolMax} · model {model}/{modelMax} · {seconds}s'`
  Call site:
```tsx
<p className="mt-1 text-xs text-muted-foreground">
  {t('status.budgetUsageMax', {
    tool: progress.toolCalls,
    toolMax: run.budget?.max_tool_calls ?? '-',
    model: progress.modelCalls,
    modelMax: run.budget?.max_model_calls ?? '-',
    seconds: progress.seconds,
  })}
</p>
```
(`status.budgetUsage` base template stays unchanged — existing consumer `RunDetailDialog.tsx:179` unaffected.)

- [ ] **Step 6: SubagentCard**:
- `:78` `加载中…` → `{t('common.loading')}`
- `:83` `重试` → `{t('common.retry')}`
- `:88` `（无子 run 记录）` → `{t('subagent.empty')}`
- `:98` `{r.status}` → `{t(`status.${r.status}`)}`
Add `import { t } from '@/i18n'` (file already imports from `@/lib/status`).

- [ ] **Step 7: Update bubble/input tests, verify**

Run: `npx vitest run src/components && npx tsc --noEmit`
Expected: PASS (update any literal assertions to `t()` values).

- [ ] **Step 8: Commit**

```bash
git add src/components/Messages/ src/components/ChatInputBar.tsx src/components/ChatInputBar.test.tsx
git commit -m "feat(frontend): message bubbles, ChatInputBar, SubagentCard i18n"
```

### Task 5: eventSummary → timeline.* lookup

**Files:**
- Modify: `frontend/src/lib/status.ts:133-198`, test `frontend/src/lib/status.test.ts`

- [ ] **Step 1: Write failing parity test**

`src/lib/status.test.ts` already imports `eventSummary`, `eventCategory`, and types `AgentEvent`/`EventType` (lines 2-3) and provides an `ev(type, payload)` helper (line 5) — reuse them; do NOT re-import. Append:

```ts
const R32_EVENT_TYPES = [
  'run.created','run.started','run.finished','run.failed','run.cancelled','run.timed_out','run.resumed',
  'model.requested','model.completed','model.token','model.failed',
  'tool.requested','tool.authorized','tool.denied','tool.started','tool.completed','tool.failed',
  'step.started','step.finished',
  'approval.requested','approval.decided',
  'plan.created','plan.updated',
  'checkpoint.saved',
  'verification.passed','verification.failed',
  'budget.consumed','budget.exceeded',
] as const

describe('eventSummary timeline parity', () => {
  it('every event type maps to its timeline.* key with empty payload', () => {
    for (const type of R32_EVENT_TYPES) {
      if (type === 'budget.consumed') continue // payload-dependent: covered below
      expect(eventSummary(ev(type))).toBe(t(`timeline.${type}`))
    }
  })

  it('budget.consumed keeps interpolation', () => {
    expect(eventSummary(ev('budget.consumed', { tool_calls: 2, model_calls: 1 })))
      .toBe(t('status.budgetUsage', { tool: '2', model: '1' }))
  })

  it('tool.completed appends tool name', () => {
    expect(eventSummary(ev('tool.completed', { tool: 'file_edit' })))
      .toBe(`${t('timeline.tool.completed')} file_edit`)
  })
})
```

Add `import { t } from '@/i18n'` at the top of the file (first new import; no existing i18n import there).

- [ ] **Step 2: Run, verify failure**

Run: `npx vitest run src/lib/status.test.ts`
Expected: FAIL — current `eventSummary` returns English prose (`'tool completed'` ≠ `t('timeline.tool.completed')` = `'工具完成'`).

- [ ] **Step 3: Rewrite eventSummary**

Replace lines 133-198 switch with lookup + payload detail:

```ts
export function eventSummary(e: AgentEvent): string {
  const p = eventPayload(e)
  const base = t(`timeline.${e.event_type}`)
  switch (e.event_type) {
    case 'tool.requested':
    case 'tool.started':
    case 'tool.completed':
    case 'tool.failed':
    case 'tool.authorized':
    case 'tool.denied':
      return p.tool ? `${base} ${String(p.tool)}` : base
    case 'run.failed':
      return p.error_code ? `${base}: ${String(p.error_code)}` : base
    case 'step.started':
    case 'step.finished':
      if (p.description) return String(p.description)
      return p.step_index != null ? `${base} ${String(p.step_index)}` : base
    case 'budget.consumed':
      return p.tool_calls != null
        ? t('status.budgetUsage', { tool: String(p.tool_calls), model: String(p.model_calls ?? 0) })
        : base
    case 'approval.requested':
      return p.tool_name ? `${base}: ${String(p.tool_name)}` : base
    case 'approval.decided':
      return base // neutral key 审批决定/Approval decided; granted/denied distinction YAGNI (payload visible in raw view)
    default:
      return base === `timeline.${e.event_type}` ? `Unknown event ${e.event_type}` : base // unknown type: keep legacy wording, never leak raw key
  }
}
```

Simplification for `approval.decided`: zh `审批决定`/en `Approval decided` are neutral — keep single base, drop the granted/denied distinction (payload `approved` is visible in raw view; YAGNI). If reviewer prefers distinction, use `p.approved ? base : base` — same result; acceptable.

Note: `budget.exceeded` key exists (`timeline.budget.exceeded` line 124/269). `model.token` exists (`timeline.model.token` '流式输出'). All 28 types covered.

- [ ] **Step 4: Run tests, verify pass**

Run: `npx vitest run src/lib/status.test.ts && npx tsc --noEmit`
Expected: PASS. Existing status.test.ts assertions on English summaries must be updated to `t()` values. (`SystemEventsBubble` has no test file — its rendering of `eventSummary` output is covered by the parity test + existing suite.)

- [ ] **Step 5: Commit**

```bash
git add src/lib/status.ts src/lib/status.test.ts
git commit -m "feat(frontend): eventSummary uses timeline.* i18n lookup with payload details"
```

### Task 6: Batch-1 acceptance — no hardcoded strings sweep

- [ ] **Step 1: Grep sweep for residual hardcoded user-visible strings**

Two passes:
1. CJK: Grep pattern `[\u4e00-\u9fff]` over `frontend/src/components` + `frontend/src/lib`, excluding `*.test.*` and `i18n/`.
2. English literals: Grep pattern `(title|placeholder|aria-label)="[A-Z]` over `frontend/src/components` + JSX text-node literals in migrated files (`>Cancel<`, `>Send<`, `>Result<` etc.).
Expected: only comments remain (comments are allowed — they are not user-visible). `NewRunDialog` mode values `'react'`/`'plan_execute'` are data, not display — display names go through `newrun.modeReact`/`newrun.modePlanExecute`.

- [ ] **Step 2: Full frontend gate**

Run (from `frontend/`): `npx tsc --noEmit && npx vitest run`
Expected: 0 type errors, full suite green.

- [ ] **Step 3: Commit any stragglers** (if sweep found missed strings): same pattern as Tasks 2-5, commit `feat(frontend): i18n sweep stragglers`. If clean, no commit.

---

## Chunk 2: Interaction Robustness (Batch 2)

### Task 7: session-store lastError + SessionList inline error row

**Files:**
- Modify: `frontend/src/store/session-store.ts`, `frontend/src/components/SessionList.tsx`
- Modify (append): `frontend/src/store/session-store.test.ts` — **file already exists with 7 passing tests** using a `vi.hoisted` api mock (`createSession`, `listSessionsPage`, `deleteSession`); append the new describe block, reuse the existing mock pattern, NEVER overwrite the file.

- [ ] **Step 1: Write failing store test**

APPEND to existing `src/store/session-store.test.ts` (reuse its `vi.hoisted` mock objects at top of file — extend the hoisted block with `renameSession: vi.fn().mockResolvedValue(undefined)` if rename tests needed):

```ts
describe('session-store lastError', () => {
  beforeEach(() => {
    createSession.mockRejectedValue(new Error('boom')) // from existing vi.hoisted block
    useSessionStore.setState({ lastError: null, sessions: [], activeSessionId: null })
  })

  it('createSession sets lastError on api failure and does not throw', async () => {
    await expect(useSessionStore.getState().createSession('t')).resolves.toBeUndefined()
    expect(useSessionStore.getState().lastError).toBe('boom')
  })

  it('clearError resets lastError', () => {
    useSessionStore.setState({ lastError: 'x' })
    useSessionStore.getState().clearError()
    expect(useSessionStore.getState().lastError).toBeNull()
  })
})
```

Note: the existing hoisted `createSession` mock defaults to `mockResolvedValue`; set `mockRejectedValue` inside this describe's `beforeEach` and reset in an `afterAll`/next-suite `beforeEach` if ordering matters (vitest runs files top-down; existing tests re-set their own mocks via their own beforeEach — verify at execution, adjust to `mockReset()` + explicit setup if pollution appears).

Also: existing test file imports `useRunStore` — session-store's `switchSession` calls it; not touched by lastError work.

- [ ] **Step 2: Run, verify failure**

Run: `npx vitest run src/store/session-store.test.ts`
Expected: FAIL — `lastError` not in state type / `clearError` not a function.

- [ ] **Step 3: Implement lastError in store**

`session-store.ts` changes:
- Interface: add `lastError: string | null` and `clearError: () => void`
- Initial state: `lastError: null`
- Wrap the 5 async actions. Pattern (createSession shown; same for loadSessions/loadMoreSessions/renameSession/deleteSession — keep existing `set` logic inside try):

```ts
createSession: async (title) => {
  try {
    const res = await api.createSession(title)
    const newSession: Session = { session_id: res.session_id, title, created_at: Date.now(), updated_at: Date.now() }
    set({ sessions: [...get().sessions, newSession], activeSessionId: res.session_id, lastError: null })
  } catch (e) {
    set({ lastError: e instanceof Error ? e.message : String(e) })
  }
},
clearError: () => set({ lastError: null }),
```

Do NOT rethrow — existing fire-and-forget callers (`App.tsx:16 void loadSessions()`, `SessionList` handlers) rely on no unhandled rejection.

- [ ] **Step 4: Write failing SessionList error-row test**

Add to `src/components/SessionList.test.tsx`:

```ts
it('renders inline error row when lastError set, dismiss clears it', async () => {
  useSessionStore.setState({ lastError: 'network down' })
  render(<SessionList />)
  expect(screen.getByText(/操作失败|Action failed/)).toBeTruthy()
  expect(screen.getByText('network down')).toBeTruthy()
  fireEvent.click(screen.getByRole('button', { name: /×|close|关闭/i }))
  expect(useSessionStore.getState().lastError).toBeNull()
})
```

- [ ] **Step 5: Run, verify failure**

Run: `npx vitest run src/components/SessionList.test.tsx`
Expected: FAIL — no error row rendered.

- [ ] **Step 6: Implement error row in SessionList**

In `SessionList.tsx`, inside the top `<div className="flex flex-col gap-2 p-3">` (after the search/create area), add:

```tsx
{lastError && (
  <div className="flex items-center gap-2 rounded border border-red-500/30 bg-red-500/10 px-2 py-1.5 text-xs text-red-400" role="alert">
    <span className="shrink-0">{t('sessionlist.actionFailed')}:</span>
    <span className="min-w-0 flex-1 truncate">{lastError}</span>
    <button
      type="button"
      aria-label={t('sessionlist.close')}
      className="shrink-0 rounded px-1 hover:bg-muted"
      onClick={() => clearError()}
    >
      ×
    </button>
  </div>
)}
```

Wire: `const lastError = useSessionStore((s) => s.lastError)` and `const clearError = useSessionStore((s) => s.clearError)`. Keys `'sessionlist.close'` ('关闭'/'Close') were added in Task 1.

- [ ] **Step 7: Run tests, verify pass**

Run: `npx vitest run src/store/session-store.test.ts src/components/SessionList.test.tsx && npx tsc --noEmit`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add src/store/session-store.ts src/store/session-store.test.ts src/components/SessionList.tsx src/components/SessionList.test.tsx src/i18n/index.ts
git commit -m "feat(frontend): session action failures visible via inline error row"
```

### Task 8: SessionList a11y — hover-only buttons keyboard-reachable

**Files:**
- Modify: `frontend/src/components/SessionList.tsx:227-243` (rename/memories/delete icon buttons), test `SessionList.test.tsx`

- [ ] **Step 1: Write failing a11y test**

Add to `SessionList.test.tsx`:

```ts
it('rename/memories/delete buttons have aria-label and focus-visible variant', () => {
  render(<SessionList />)
  const checks: [RegExp, string][] = [
    [/重命名|rename/i, 'sessionlist.renameTitle'],
    [/记忆|memor/i, 'sessionlist.memoriesTitle'],
    [/删除|delete/i, 'sessionlist.deleteTitle'],
  ]
  for (const [name, key] of checks) {
    const btn = screen.getAllByRole('button', { name }).find(Boolean)
    expect(btn, key).toBeTruthy()
    expect(btn?.getAttribute('aria-label')).toBe(t(key))
    expect(btn?.className).toContain('focus-visible:flex')
  }
})
```

Note: `title` alone may already yield an accessible name in some testing-library versions — the `aria-label` + `focus-visible:flex` assertions are the load-bearing ones and fail today unconditionally. Add `import { t } from '@/i18n'` to the test file if absent.

- [ ] **Step 2: Run, verify failure**

Run: `npx vitest run src/components/SessionList.test.tsx`
Expected: FAIL (no aria-label) or skip-if-pass per note above.

- [ ] **Step 3: Implement**

On each of the three buttons (rename `:227`, memories `:235`, delete `:241`):
- Add `aria-label={t('sessionlist.renameTitle')}` / `t('sessionlist.memoriesTitle')` / `t('sessionlist.deleteTitle')` (title keys already exist)
- className: `hidden ... group-hover:flex` → `hidden ... group-hover:flex focus-visible:flex` (Tailwind `focus-visible:flex` sets `display:flex` on keyboard focus, overriding `hidden` via variant specificity)

Example (rename button):

```tsx
<button
  onClick={() => startRename(s.session_id, s.title || s.session_id)}
  title={t('sessionlist.renameTitle')}
  aria-label={t('sessionlist.renameTitle')}
  className="hidden h-6 w-6 shrink-0 items-center justify-center rounded text-muted-foreground/70 hover:bg-muted hover:text-foreground group-hover:flex focus-visible:flex"
>
```

- [ ] **Step 5: SessionList run badge i18n (spec gap #10 part 1)**

`SessionList.tsx:285` renders raw `{run.status}` in the run sub-list. Replace with the same pattern as RunsHistoryDialog (import `KNOWN_STATUSES` from `@/lib/status` or add it there if not exported — verify; RunsHistoryDialog declares it locally at :29-38, so export it from `lib/status.ts` and have RunsHistoryDialog import it, removing its local copy):

```tsx
{KNOWN_STATUSES.has(run.status) ? t(`status.${run.status}`) : run.status}
```

Add failing assertion to `SessionList.test.tsx` first (a run fixture with `status: 'running'` renders `t('status.running')` = 运行中, not raw `running`).

- [ ] **Step 6: Run tests, verify pass**

Run: `npx vitest run src/components/SessionList.test.tsx && npx tsc --noEmit`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add src/components/SessionList.tsx src/components/SessionList.test.tsx src/lib/status.ts src/components/RunsHistoryDialog.tsx
git commit -m "feat(frontend): SessionList icon buttons keyboard-reachable (aria-label + focus-visible); run badge i18n"
```

### Task 9: RunsHistoryDialog filter chips — all 8 statuses

**Files:**
- Modify: `frontend/src/components/RunsHistoryDialog.tsx:58-65`, test `RunsHistoryDialog.test.tsx`

- [ ] **Step 1: Write failing test**

Add to `RunsHistoryDialog.test.tsx`:

```ts
it('filter chips cover all 8 run statuses plus All', () => {
  render(<RunsHistoryDialog open onOpenChange={() => {}} />)
  for (const label of [/全部|All/, /排队中|Queued/, /运行中|Running/, /待审批|Awaiting approval/, /已暂停|Paused/, /成功|Succeeded/, /失败|Failed/, /已取消|Cancelled/, /超时|Timed out/]) {
    expect(screen.getAllByText(label).length).toBeGreaterThan(0)
  }
})
```

(Props are `open: boolean` + `onOpenChange: (v: boolean) => void` — RunsHistoryDialog.tsx:41-45; there is no `onClose` prop.)

- [ ] **Step 2: Run, verify failure**

Run: `npx vitest run src/components/RunsHistoryDialog.test.tsx`
Expected: FAIL — `running/waiting_approval/queued/paused` chips absent.

- [ ] **Step 3: Implement**

Replace `statusChips` (lines 58-65):

```ts
const statusChips: [string, string][] = [
  ['', t('runs.filterAll')],
  ...(['queued', 'running', 'waiting_approval', 'paused', 'succeeded', 'failed', 'cancelled', 'timed_out'] as const)
    .map((s): [string, string] => [s, t(`status.${s}`)]),
]
```

Order mirrors `KNOWN_STATUSES` (lines 29-38) and backend state machine. Filter comparison logic below unchanged — it already compares against `r.status`.

- [ ] **Step 4: Run tests, verify pass**

Run: `npx vitest run src/components/RunsHistoryDialog.test.tsx && npx tsc --noEmit`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add src/components/RunsHistoryDialog.tsx src/components/RunsHistoryDialog.test.tsx
git commit -m "feat(frontend): run history filter chips cover all 8 statuses"
```

### Task 10: Batch-2 acceptance gate

- [ ] **Step 1:** Run `npx tsc --noEmit && npx vitest run` from `frontend/`. Expected: 0 errors, all green.
- [ ] **Step 2:** No commit needed if green (per-task commits already made).

---

## Chunk 3: Cleanup (Batch 3)

### Task 11: Dead code removal + CHANGELOG

**Files:**
- Delete: `frontend/src/components/Messages/index.ts` (zero importers — verified by spec review: ChatMessageList.tsx:9-16 imports concrete files)
- Delete (conditional): `frontend/src/components/ui/skeleton.tsx` — ONLY if `grep -rn "skeleton" frontend/src --include="*.tsx" --include="*.ts"` (Grep tool) shows no importer outside the file itself
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Verify Messages/index.ts zero importers**

Run (Grep tool): pattern `from '@/components/Messages'` in `frontend/src`.
Expected: 0 hits. If hits exist, STOP — update importers to concrete files first, then delete barrel.

- [ ] **Step 2: Delete Messages/index.ts**

```bash
git rm frontend/src/components/Messages/index.ts
```

- [ ] **Step 3: Verify skeleton.tsx importers, delete if dead**

Run (Grep tool): pattern `ui/skeleton` in `frontend/src`.
If only self-references → `git rm frontend/src/components/ui/skeleton.tsx`. Else leave file untouched.

- [ ] **Step 4: Run full gate**

Run (from `frontend/`): `npx tsc --noEmit && npx vitest run`
Expected: 0 errors, all green (deletion broke nothing).

- [ ] **Step 5: CHANGELOG R32 entry**

Add entry at top of `CHANGELOG.md` following existing format (read the file's latest entry for format — likely `- R32 (2026-11-05): ...`):

```markdown
- R32 (2026-11-05): 前端 i18n 全面收口（NewRunDialog/SettingsDialog/ApprovalCard/消息气泡/ChatInputBar/SubagentCard/eventSummary → timeline.* 查表），session 操作失败内联错误提示，SessionList 图标按钮键盘可达 + aria-label，运行历史筛选补全 8 状态，移除死代码（Messages barrel、skeleton 如无引用）。
```

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "chore(frontend): remove dead code (Messages barrel, unused skeleton), CHANGELOG R32"
```

---

## Execution Notes

- **Reference skills:** @superpowers:subagent-driven-development (execution), @superpowers:verification-before-completion (final gate).
- **Lang in tests:** default UI lang is zh (check `useUiStore` initial state at execution time); tests asserting rendered text should use `t()` directly or zh/en alternation regexes as shown.
- **`t()` fallback semantics:** missing key returns the key string itself (zh fallback then key) — parity tests rely on `t(resolvedKey) !== key` to catch unresolvable keys.
- **No backend changes, no eval changes, no README eval-count changes.**
- **Risk watch:** `focus-visible:flex` + `hidden` combination — verify visually only if test suite can't; `getAllByText` ambiguity in RunsHistoryDialog test if status strings appear in multiple rows (use `getAllByText` with length assertion, as written).
- **Commits:** plan uses per-task commits (finer than spec §4.4's per-batch); batch acceptance gates (Task 6/10) guarantee each batch is independently green. Do not squash.
