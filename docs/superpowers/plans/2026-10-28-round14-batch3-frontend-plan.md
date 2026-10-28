# Round 14 批3 实现计划：前端 i18n 收尾 + 运行详情视图

> **For agentic workers:** REQUIRED: Use superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 收尾剩余 i18n 硬编码（2 处 '加载失败'）；新增 RunDetailDialog 聚合视图（steps 时间线 / 工具调用明细 / 审批记录），从运行历史弹窗行点击打开。

**Architecture:** 复用既有 Dialog 模式（fetch-on-open + lang 订阅 + 错误重试）；数据三路并行 `getRun`（已有，返回 {run, steps}）+ `getEvents(runId, 0)`（已有）+ `listApprovals`（新增）；工具调用明细来自 tool.completed 事件的 payload（R5 载荷增强，含 args/output）。

**Tech Stack:** React 18 + TypeScript + Tailwind + zustand（无新依赖）。

**Spec:** docs/superpowers/specs/2026-10-28-round14-observe-evals-frontend-design.md §3.3

---

## 已验证事实

- `common.loadFailed` 键已存在（i18n/index.ts zh L10 / en L63）→ 只需替换字符串 + 补 import/lang 订阅。
- ToolsPanel.tsx:34 与 SettingsDialog.tsx:41：`e instanceof ApiError ? e.message : '加载失败'`；两文件均未 import t/useUiStore。
- api.ts：`getRun(id)` 返回 `{run, steps}`（L128）；`getEvents(runId, afterSeq)` 返回 AgentEvent[]（L134）；**无 listApprovals** → 新增（GET /runs/{id}/approvals → `{approvals}`，handlers_runs.go:270）。
- Approval 类型已存在（types.ts L102-114：tool_name/status/reason/created_at）。
- Dialog 模式参照 MemoriesDialog（fetch-on-open、`useUiStore((s) => s.lang)` 裸订阅、错误重试、shadcn Dialog）。
- RunsHistoryDialog 行为 div（L103），点击处理将挂到整行。
- 状态徽章复用 `runStatusClasses` / `stepStatusClasses`（lib/status.ts）。

---

### Task 1: i18n 收尾（2 处硬编码）

**Files:**
- Modify: `frontend/src/components/ToolsPanel.tsx:34`
- Modify: `frontend/src/components/SettingsDialog.tsx:41`

- [ ] **Step 1: 替换 + 补 import/订阅**

两文件各加：
```ts
import { t } from '@/i18n'
import { useUiStore } from '@/store/ui-store'
```
组件体内加（lang 订阅，裸表达式触发重渲染）：
```ts
  useUiStore((s) => s.lang)
```
`'加载失败'` → `t('common.loadFailed')`。

- [ ] **Step 2: 验证**

Run: `cd frontend && npx tsc --noEmit && npx vitest run`
Expected: tsc 0 错误；既有测试全绿（无行为变化）。

- [ ] **Step 3: Commit**

```bash
git add frontend/src/components/ToolsPanel.tsx frontend/src/components/SettingsDialog.tsx
git commit -m "feat(frontend): finish i18n migration (remaining load-failed strings)"
```

---

### Task 2: api.listApprovals + RunDetailDialog

**Files:**
- Modify: `frontend/src/lib/api.ts`（新增 listApprovals）
- Create: `frontend/src/components/RunDetailDialog.tsx`
- Create: `frontend/src/components/RunDetailDialog.test.tsx`
- Modify: `frontend/src/i18n/index.ts`（rundetail.* 键）

- [ ] **Step 1: i18n 键（zh/en 各一组）**

```ts
'rundetail.title': '运行详情',
'rundetail.steps': '执行步骤',
'rundetail.toolCalls': '工具调用',
'rundetail.approvals': '审批记录',
'rundetail.budget': '预算用量',
'rundetail.emptySteps': '（无步骤记录）',
'rundetail.emptyToolCalls': '（无工具调用）',
'rundetail.emptyApprovals': '（无审批记录）',
// en: 'Run detail' / 'Steps' / 'Tool calls' / 'Approvals' /
//     'Budget usage' / '(no steps)' / '(no tool calls)' / '(no approvals)'
```

- [ ] **Step 2: 失败测试（RunDetailDialog.test.tsx）**

vi.mock '@/lib/api'：getRun 返回 {run: run('r1','succeeded'), steps:[{id:'st1',run_id:'r1',step_index:1,description:'搜索资料',status:'succeeded'}]}；getEvents 返回 [{seq:3,type:'tool.completed',payload:JSON.stringify({args:'{"query":"q"}',output:'result-body'})}]；listApprovals 返回 [{id:'a1',run_id:'r1',tool_call_id:'tc1',tool_name:'file_edit',args_hash:'h',requested_by:'agent',status:'approved',expires_at:0,created_at:1}]。
断言：打开时调用三个 API；渲染 '搜索资料'、'file_edit'、'result-body'（工具调用区）、'approved'；runId=null 不调用。错误路径：getRun reject → 'common.loadFailed' + 重试按钮。

- [ ] **Step 3: 运行确认失败**

Run: `cd frontend && npx vitest run src/components/RunDetailDialog.test.tsx`
Expected: FAIL（组件不存在）

- [ ] **Step 4: api.listApprovals + 组件实现**

api.ts（getEvents 后）：
```ts
  async listApprovals(runId: string): Promise<Approval[]> {
    const data = await this.request<{ approvals: Approval[] }>(
      'GET',
      `/api/v1/runs/${encodeURIComponent(runId)}/approvals`,
    )
    return data.approvals ?? []
  }
```

RunDetailDialog.tsx：Props `{ runId: string | null; onOpenChange: (v: boolean) => void }`；open = runId != null；load() 用 Promise.all 三路并发，任一失败置 error；区块：头部（状态徽章 runStatusClasses + goal + error_code 红字）、预算（t('status.budgetUsage', {tool, model})）、steps 列表（step_index + description + stepStatusClasses 徽章）、工具调用（type==='tool.completed' 且 payload 可解析出 args/output 的事件，`<pre>` 展示 + max-h 截断）、审批（tool_name + status + reason）。空态文案用 rundetail.empty*。

- [ ] **Step 5: 运行确认通过**

Run: `cd frontend && npx vitest run src/components/RunDetailDialog.test.tsx && npx tsc --noEmit`
Expected: PASS + tsc 0。

- [ ] **Step 6: Commit**

```bash
git add frontend/src/lib/api.ts frontend/src/i18n/index.ts frontend/src/components/RunDetailDialog.tsx frontend/src/components/RunDetailDialog.test.tsx
git commit -m "feat(frontend): run detail dialog with steps, tool calls and approvals"
```

---

### Task 3: 运行历史行点击 + docs + 验收

- [ ] **Step 1: RunsHistoryDialog 行点击打开详情**

State: `detailRunId: string | null`；行 div 加 `cursor-pointer` + `onClick={() => setDetailRunId(r.id)}`；渲染 `<RunDetailDialog runId={detailRunId} onOpenChange={(v) => !v && setDetailRunId(null)} />`。测试：点击行 → RunDetailDialog mock 收到 runId（或断言弹窗内出现 goal）。

- [ ] **Step 2: 验证 + 全量前端回归**

Run: `cd frontend && npx tsc --noEmit && npm run lint && npx vitest run`
Expected: 全绿。

- [ ] **Step 3: docs zh/en + 后端回归**

frontend/README.md / README.en.md 组件行 += 运行详情弹窗（steps/工具调用/审批聚合）。后端回归：`cd backend && go build ./... && go test ./... -count=1`。

- [ ] **Step 4: Commit**

```bash
git add frontend/src/components/RunsHistoryDialog.tsx frontend/src/components/RunsHistoryDialog.test.tsx frontend/README.md frontend/README.en.md
git commit -m "feat(frontend): open run detail from run history rows (zh/en docs)"
```
