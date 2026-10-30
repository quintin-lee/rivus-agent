# Round 25 实施计划：oauth 文档 + evals 36 + 前端打磨

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 补 settings.md oauth 文档（R24 遗留）、evals 34→36（FTS 中文 + oauth 拒绝）、前端复制按钮 + 聊天空状态。

**Architecture:** 文档纯编辑；evals 沿 `Run(t, Scenario{...})` PostCheck 风格；前端复制按钮为组件内 `useCopy` 小 hook（copied 状态 1.5s 复位），空状态经 ui-store `newRunOpen` 打开 ChatInputBar 中的 NewRunDialog（本地 state 上移）。

**Tech Stack:** Go 1.27 / React 18 + Zustand + Vitest。无新依赖。

**Spec:** docs/superpowers/specs/2026-10-29-round25-oauth-docs-evals-frontend-design.md（bb0473a）

---

## Chunk 1

### Task 1: settings.md oauth 文档（zh/en）

**Files:** `docs/api/settings.md`（L26-44 区域）、`docs/en/api/settings.md`

- [ ] PUT `mcpServers` 行后补 oauth 说明（仅 type:http；{token_url, client_id, client_secret, scopes?}；缺字段 400）+ 带 oauth 的 PUT curl
- [ ] GET 响应 servers 对象补 `oauth` 回显字段（{token_url, client_id, scopes?, has_secret}；client_secret 永不回显，与 headers 同规则）+ GET 示例片段
- [ ] 验证：`grep -c oauth docs/api/settings.md docs/en/api/settings.md` 各 ≥3；commit `docs(api): mcp server oauth fields (zh/en)`

### Task 2: evals 34→36（TDD：先写场景跑失败→实现→绿）

**Files:** 新建 `backend/tests/evals/scenarios_round25_test.go`；`backend/README.md` + `backend/README.en.md` 计数

- [ ] `run_search_fts_cjk`：PostCheck 造 goal='r25 全文检索 中文资料 xyz' 的 run → `GET /api/v1/runs?q=中文资料` 仅命中它；`?q=xyz` 也命中同一 run；错词不命中
- [ ] `mcp_oauth_stdio_rejected`：PUT mcpServers=[{name:'x',command:'echo',oauth:{token_url,client_id,client_secret}}] → 400 + 'oauth is only supported for http servers'；同 payload 去 oauth → 200
- [ ] README 计数 34→36（工具级 22 + agent 级 14，zh L144/en L148）+ 文件列表行；commit `test(evals): fts cjk search and oauth rejection scenarios (34 -> 36)`

### Task 3: 前端复制按钮（TDD）

**Files:** `frontend/src/components/Messages/ToolCallsCard.tsx`、`frontend/src/components/RunDetailDialog.tsx`、`frontend/src/i18n/index.ts`、`frontend/src/lib/useCopy.ts`（新建）

- [ ] RED：ToolCallsCard.test（mock clipboard.writeText，点击 output 区 Copy 按钮 → writeText 调用 + Check 图标出现）+ RunDetailDialog.test（run ID 旁 Copy → writeText('r1abcdef')）
- [ ] 实现：`useCopy()` → {copied, copy(text)}（clipboard.writeText + copied=true + 1.5s 复位；clipboard 不可用时 copy 为 no-op）；两处按钮（Copy/Check lucide 图标，aria-label t('common.copy')）；i18n common.copy/copy 复制·Copy、common.copied 已复制·Copied
- [ ] tsc/lint/相关测试绿；commit `feat(frontend): copy buttons for tool output and run id`

### Task 4: 聊天空状态（TDD）

**Files:** `frontend/src/store/ui-store.ts`、`frontend/src/components/ChatInputBar.tsx`、`frontend/src/components/ChatMessageList.tsx`、i18n

- [ ] RED：ChatMessageList.test（无 messages → 空状态文案 + 点击按钮 → ui-store.setNewRunOpen(true)）；ui-store.test（setNewRunOpen 置位）
- [ ] 实现：ui-store += `newRunOpen: boolean; setNewRunOpen(v: boolean)`；ChatInputBar dialogOpen 改用 store（`open={newRunOpen}` / onOpenChange=setNewRunOpen，保留本地关闭逻辑同步）；ChatMessageList `messages.length===0` → 居中卡片（Bot 图标 + t('chat.empty') + 按钮 t('chat.emptyCta') onClick setNewRunOpen(true)）；i18n chat.empty 还没有运行，创建第一个试试 / No runs yet — create your first one，chat.emptyCta 新建运行 / New run
- [ ] 全量前端回归；commit `feat(frontend): chat empty state opens new-run dialog via ui-store`

### Task 5: 全量验收 + 偏差回写

- [ ] backend go build/vet/test 19 pkgs + make test-eval 36 pass_rate 1；frontend tsc/lint/vitest
- [ ] spec §7 偏差回写 + commit；wrap-up 报告

---

## 自查笔记

1. NewRunDialog 开关在 ChatInputBar 本地 state（L85）——空状态按钮需 ui-store 中转（Task 4 设计）。
2. ToolCallsCard output 块 L37-39（有条件渲染）——Copy 按钮放 pre 内右上角，仅在 output 存在时渲染。
3. RunDetailDialog 标题区（DialogTitle 附近）——run ID 显示处加复制；具体行号执行时以实际 JSX 为准。
4. evals Run() 框架：Scenario{Category, Script EnqueueText, Want succeeded MaxModelCalls 1, PostCheck}；R19/R21 先例 `Run(t, Scenario{...})` 顶层函数式。
5. clipboard 在 jsdom 不可用：mock `navigator.clipboard = { writeText: vi.fn() }`（需在测试里 stub global）。
