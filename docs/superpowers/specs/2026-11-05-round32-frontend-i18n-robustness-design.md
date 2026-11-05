# Round 32 设计：前端 i18n 收口 + 交互健壮性

日期：2026-11-05 ｜ 状态：已批准（'批准，写设计稿'）

## 1. 背景

R31 后前端扫描发现 10 个缺口，主导问题是 **i18n 不完整**：应用已有完整 zh/en 字典（`src/i18n/index.ts`），但四个最核心的用户界面绕过 `t()` 用硬编码字符串，中英混杂。次要问题：session 操作失败静默（fire-and-forget 无 catch）、SessionList 悬停按钮键盘不可达、RunsHistoryDialog 状态筛选缺常用态、一处已验证死代码。

具体缺口（scout 扫描 + 人工复核）：

| # | 位置 | 问题 |
|---|------|------|
| 1 | `NewRunDialog.tsx`（53,55,61,65,72,94,96,99,102,106,142） | 整个对话框硬编码英文，从不调 `t()` |
| 2 | `lib/budget.ts:12-16` | BUDGET_FIELDS label/hint 硬编码 zh（`时长上限（秒）`等），泄漏到 en UI |
| 3 | `SettingsDialog.tsx`（76,82,84,89,102,115,123,129） | 硬编码 zh（`模型设置`/`供应商`/`已设置`/`留空表示不修改`等），无 `settings.*` 键 |
| 4 | `ApprovalCard.tsx`（29-32,45-49,54,63,67,79,84,88,93） | 审批流全英文（badge/Approve/Reject/对话框） |
| 5 | `AssistantBubble.tsx`（16,18,27,33,41） | `Cancelled — partial result`/`Run failed`/`Result` 硬编码英文 |
| 6 | `UserBubble.tsx`（20,24）、`ChatInputBar.tsx`（~34,72,81） | `Sending…`/`Failed:`/placeholder/`Send` 硬编码英文 |
| 7 | `SystemStatusBubble.tsx:40,111` | 状态徽章渲染 raw `run.status.replace('_',' ')`；用量行硬编码 zh `工具 x/y · 模型 x/y · Ns` |
| 8 | `SystemEventsBubble.tsx`（22,52,60）+ `lib/status.ts:133-198 eventSummary` | `{n} events`/`[masked]`/`hide`/`raw` 硬编码；eventSummary 每个分支返回英文串，与已齐的 `timeline.*` 字典重复 |
| 9 | `SubagentCard.tsx`（78,83,88,98） | `加载中…`/`重试`/`（无子 run 记录）`硬编码 zh，尽管 `common.loading`/`common.retry` 已存在；状态徽章 raw |
| 10 | `SessionList.tsx:285` 状态徽章 raw；217-241 悬停按钮 `hidden … group-hover:flex`（键盘不可达、触屏不可见）仅 `title=` 无 `aria-label` |
| 11 | `session-store.ts`（23-24,46-54） | `createSession`/`loadSessions`/`loadMoreSessions`/`renameSession` 无 try/catch；调用点 fire-and-forget（`App.tsx:11` `void loadSessions()`、`SessionList.tsx:119-128` handleCreate 有 finally 无 catch）→ 失败静默，输入框文字保留无提示 |
| 12 | `RunsHistoryDialog.tsx:58-65` | 筛选 chips 仅 All/succeeded/failed/cancelled/timed_out，缺 running/waiting_approval/queued/paused |
| 13 | `components/Messages/index.ts` | barrel 死代码：grep 零引用，ChatMessageList 直接按文件导入 |

## 2. 方案（3 批串行，每批独立 commit + 验证）

> 已评估的替代方案：B 单大批次（验证面 ~15 文件一次铺开，失败定位成本高）；C 极简版只做 i18n+session 错误（悬停按钮触屏不可见是真实可用性缺陷）。选 A：按风险分层，最大面最低风险的 i18n 先行。

### 批1 i18n 机械迁移（最大面、最低风险）

**原则：先复用现有键（`common.*`/`status.*`/`timeline.*`），缺的才新增。**

- **硬编码组件迁移**（每个组件一个命名空间，zh+en 同步加键）：
  - `NewRunDialog.tsx` → `newrun.*`；`SettingsDialog.tsx` → `settings.*`；`ApprovalCard.tsx` → `approval.*`
  - `AssistantBubble`/`UserBubble`/`ChatInputBar`/`SystemEventsBubble` → `bubble.*`/`input.*`/`events.*`
  - `SubagentCard`：改用已存在的 `common.loading`/`common.retry`，仅新增「无子 run 记录」一键；状态徽章走 `status.*`
  - `lib/budget.ts` 的 zh 标签 → `budget.*` key（label/hint 变 key 名），渲染层（NewRunDialog）用 `t()` 取
- **状态徽章统一**：`SessionList.tsx:285`、`SystemStatusBubble.tsx:40` 套用现成模式（照抄 `RunsHistoryDialog.tsx:187`）：
  `KNOWN_STATUSES.has(s) ? t(\`status.${s}\`) : s`
- **`SystemStatusBubble:111` 用量行**：新键 `status.budgetLive`（zh `用量 工具 {tool}/模型 {model} · {seconds}s` / en `Usage tool {tool}/model {model} · {seconds}s`），替换硬编码拼串。
- **`lib/status.ts` eventSummary**：英文 switch 分支全部改为 `t(\`timeline.${e.event_type}\`)` 查表（33 个 `timeline.*` 键 zh/en 已齐，R31 落地，覆盖全部事件类型），payload 细节保留追加：`tool.*` 事件追加工具名、`run.failed` 追加 error_code、`step.*` 无 description 时用 step_index。
- **测试（TDD）**：
  - 新增 parity 测试：遍历 TS 侧已知事件类型数组，断言无 payload 时 `eventSummary(e) === t('timeline.' + type)`，防字典漂移。
  - 现有组件测试补 i18n 断言（如 NewRunDialog 渲染后无硬编码英文文案可寻）。

### 批2 交互健壮性

- **session 错误可见**：`session-store.ts` 增 `lastError: string | null` + `clearError()`；五个操作（create/load/loadMore/rename/delete）包 try/catch 写 `lastError`（不 rethrow，兼容现有 `void` fire-and-forget 调用点，不新增调用点负担）。`SessionList` 顶部渲染内联错误行：`t('sessionlist.actionFailed')` + 消息 + × 关闭（调 `clearError()`）。**不引入 toast 新基建**（`ui/` 下现无 toast 组件；内联错误行与现有 `deleteError` 模式一致，boring 安全）。
- **a11y**：SessionList 三个悬停图标按钮（rename/memories/delete）`group-hover:flex` → 加 `focus-visible:flex` + `aria-label={t('sessionlist.*Title')}`（title 键已存在）。键盘 Tab 可达、触屏聚焦可见。
- **RunsHistoryDialog 筛选补全**：chips 从 All + 4 终态扩为 All + 8 态全量，标签用 `t('status.*')`（`RunsHistoryDialog:187` 已在用，chips 行对齐）。
- **测试（TDD）**：session-store 失败路径（mock api reject → `lastError` 置位且不 unhandled rejection）、SessionList 错误行渲染与关闭、筛选 chips 全量渲染断言。

### 批3 清理

- 删 `components/Messages/index.ts`（已验证零引用）。
- `ui/skeleton.tsx` 实现时 grep 复核，零引用才删（scout 报告截断，未确认）。
- CHANGELOG 加 R32 条目。README evals 计数不变，不动。

## 3. YAGNI

不做：toast/通知系统、react-aria 类库、全局错误 store、后端改动、事件 payload 契约变更、i18n 框架替换、时间线虚拟滚动、`formatTimestamp` 的 locale 适配（现有 en-GB 固定格式对 zh 用户可接受，非本轮问题）。

## 4. 验收

1. 前端 `npx tsc --noEmit` 0 错；`npx vitest run` 全绿（新增测试计入）。
2. 全 UI 无硬编码用户可见字符串（grep 组件层中文/英文文案字面量，i18n/index.ts 与测试文件除外）。
3. `npm run lint` 现状对所有人坏（ESLint 10 需 flat config，仓库只有 legacy `.eslintrc.cjs`）——非本轮回归，不作验收门槛。
4. 每批独立 commit：批1 `feat(frontend): i18n full coverage`、批2 `feat(frontend): session error visibility + a11y + filter`、批3 `chore(frontend): dead code removal`。

## 5. 风险

- eventSummary 改写的唯一消费者是 `SystemEventsBubble`（grep 确认）——parity 测试兜底字典漂移。
- 新增约 50-60 个 key × 2 语言，纯机械但 diff 大；按组件命名空间收紧控制。
- `focus-visible:flex` 与 Tailwind `hidden` 的组合需实测（`focus-visible:flex` 特异性高于 `hidden` 的 display:none，常规做法，风险低）。
- SessionList.test.tsx 现有断言可能依赖悬停按钮的默认隐藏——实现时以 `focus-visible` 触发或直接断言 aria-label。
