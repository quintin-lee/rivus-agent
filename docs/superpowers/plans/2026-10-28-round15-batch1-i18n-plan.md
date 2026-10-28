# Round-15 批1：前端 i18n 收官 + react-hooks lint 实施计划

> **For agentic workers:** REQUIRED: Use superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 迁移最后 3 个组件的硬编码中文到 i18n，并把 react-hooks 规则纳入 lint 门禁。

**Architecture:** 沿用 R13 i18n 模式（`t()` + 裸 `useUiStore((s)=>s.lang)` 订阅 + `{n}` 占位符）；react-hooks 插件加入既有 flat config，rules-of-hooks 为 error（挡门禁）、exhaustive-deps 为 warn（不阻塞）。

**Tech Stack:** React 18 / vitest / ESLint 10 flat config / eslint-plugin-react-hooks。

**Spec:** docs/superpowers/specs/2026-10-28-round15-i18n-filter-evals-design.md §3.1

---

## Chunk 1

### Task 1: i18n 新键 + 3 组件迁移

**Files:**
- Modify: `frontend/src/i18n/index.ts`（zh dict + en dict 各加 7 键）
- Modify: `frontend/src/components/Messages/SubagentCard.tsx`（L40 加载失败、L61 子 agent ×N、刷新 title）
- Modify: `frontend/src/components/Messages/ToolCallsCard.tsx`（L22 工具调用 ×N）
- Modify: `frontend/src/components/SettingsDialog.tsx`（保存失败/保存中…/保存/关闭）
- Test: 既有 3 个组件测试断言用中文且默认 lang=zh → 无需改；新增 1 个 en 态用例

- [ ] **Step 1: 加键**（zh）：

```ts
  'common.refresh': '刷新',
  'common.close': '关闭',
  'subagent.cardTitle': '子 agent ×{n}',
  'toolcalls.cardTitle': '工具调用 ×{n}',
  'settings.saveFailed': '保存失败',
  'settings.saving': '保存中…',
  'settings.save': '保存',
```

（en 对应：Refresh / Close / `Sub-agents ×{n}` / `Tool calls ×{n}` / Failed to save / Saving… / Save）

- [ ] **Step 2: 迁移组件**——每个组件：`import { t } from '@/i18n'; import { useUiStore } from '@/store/ui-store';` + 函数体内裸订阅 `useUiStore((s) => s.lang);` + 字符串替换为 `t('common.loadFailed')` / `t('subagent.cardTitle', { n: message.count })` 等。

- [ ] **Step 3: en 态用例**——ToolCallsCard.test.tsx 加一例：`useUiStore.setState({ lang: 'en' })` → 断言 `Tool calls ×2`。

- [ ] **Step 4: 验证**：`cd frontend && npx tsc --noEmit && npx vitest run`（全绿，既有中文断言在 zh 默认下不变）。

- [ ] **Step 5: Commit** `feat(frontend): migrate remaining hardcoded strings to i18n (subagent/toolcalls/settings)`

### Task 2: react-hooks 插件

**Files:**
- Modify: `frontend/package.json`（npm i -D）
- Modify: `frontend/eslint.config.js`

- [ ] **Step 1**: `cd frontend && npm install -D eslint-plugin-react-hooks --legacy-peer-deps`
- [ ] **Step 2**: flat config 加：

```js
import reactHooks from 'eslint-plugin-react-hooks';
// plugins 里加 reactHooks；rules 里加：
  'react-hooks/rules-of-hooks': 'error',
  'react-hooks/exhaustive-deps': 'warn',
```

- [ ] **Step 3**: `npm run lint` —— 修复所有 error 级违规（warn 只记录不修）。
- [ ] **Step 4**: `npx tsc --noEmit && npx vitest run` 回归全绿。
- [ ] **Step 5: Commit** `chore(frontend): enable react-hooks eslint rules (rules-of-hooks error, exhaustive-deps warn)`

## Self-review 备注

- SettingsDialog L137 三元 `{busy ? t('settings.saving') : t('settings.save')}`。
- SubagentCard 刷新按钮：`title={t('common.refresh')}`。
- 组件既有测试断言（子 agent ×2 / 工具调用 ×2 / 保存）在 zh 默认下经 t() 渲染结果不变 → 无需改断言；boom 分支 `/boom|加载失败/` 同理。
- npm 装 plugin 需 `--legacy-peer-deps`（R11 教训：typescript peer 冲突）。
