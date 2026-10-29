# Round 22 设计：文档补遗 + Run 时间线 + 游标分页

日期：2026-10-29 ｜ 状态：已批准（用户选 '4' 全套，m2507；设计已呈现）

## 1. 背景

R21 为 GET /runs 加了 goal 搜索（q 参数），但 docs/api/runs.md zh/en 未记录（0 提及）；CHANGELOG 缺 run 目标搜索条目。RunDetailDialog 为聚合表格（步骤/工具调用/审批），无按事件时间的可视化。R20 引入的 offset 分页在深翻页时成本线性增长（OFFSET N 每页跳过 N 行）。

## 2. 批 1：文档补遗（纯文档）

- docs/api/runs.md 与 docs/en/api/runs.md：GET /api/v1/sessions 的 GET /api/v1/runs 段补 `q` 参数（大小写不敏感 goal 子串过滤，省略=不过滤）+ curl 示例，文风对齐 sessions.md 的 q 参数小节。
- CHANGELOG.md [Unreleased] Added += 「运行目标搜索：GET /runs?q= 按目标大小写不敏感子串过滤 + 前端运行历史搜索框（200ms 防抖）」。

验收：runs.md zh/en 中 `?q=` 提及 ≥1；CHANGELOG 含目标搜索条目。

## 3. 批 2：Run 时间线可视化（前端）

RunDetailDialog 已拉取 events（`getEvents(runId, 0)`），新增「时间线」区块：

- 垂直时间线：每事件一节点（左侧竖线 + 圆点，事件类型着色沿用 runStatusClasses 语义色；右侧 relTime）。
- 事件类型 → 标签映射（i18n `timeline.*` 键，zh/en 双语；未覆盖类型显示原始 type 字符串）：run.started / run.finished / run.failed / run.cancelled / run.timed_out / model.completed / model.token / model.failed / tool.completed / tool.failed / approval.requested / budget.consumed / budget.exceeded / verification.passed / verification.failed。
- 截断：只渲染最近 50 条（事件列表本身 newest-last，取末尾 50），顶部计数行「共 N 条事件 / N events」。
- 空态：无事件时显示 timeline.empty 文案。
- 测试（TDD）：渲染已知事件产生对应标签 + 计数行；空 events → 空态。

## 4. 批 3：游标分页（与 offset 并存，向后兼容）

### 4.1 后端

- store 新增 `encodeCursor(sortValue int64, id string) string`（base64 RawURLEncoding of `sortValue:id`）与 `decodeCursor(s string) (int64, string, error)`。
- `ListSessions(ctx, ownerID, q, limit, offset, cursor)`：cursor 非空时忽略 offset，SQL 追加 `AND (updated_at < ? OR (updated_at = ? AND id < ?))`；排序不变（updated_at DESC）。
- `ListRunsBySession` / `ListRunsByOwner`：同上，游标键为 `created_at`（排序键一致）。
- Handler（handleListSessions / handleListRuns 两路径）：读 `cursor` query；decode 失败 → 400 bad_request；响应在 `"sessions"` / `"runs"` 旁增加 `"next_cursor"`（仅当返回行数 == limit 时携带；无更多页时省略）。响应新字段向后兼容。
- 测试：store 单测（cursor 逐页翻完全部行且顺序 = 全量列表顺序；无效 cursor 报错）；集成（第 1 页带 next_cursor → 用其翻第 2 页 → 无重叠无遗漏；无效 cursor 400）。

### 4.2 前端

- api.ts：`listSessions(q?, offset=0, cursor?)` / `listRuns(sessionId?, limit=50, status?, offset=0, q?, cursor?)` —— cursor 非空拼 `&cursor=`；响应类型加 `next_cursor?: string`，各列表方法返回 cursor（返回结构改为 `{items, nextCursor}` 或第二返回值，实现时选改动最小者并记录偏差）。
- SessionList load-more：改传 store 中的 `sessionsCursor`（store fetch action 保存 next_cursor；offset>0 append 模式不变，cursor 模式同样 append）。
- RunsHistoryDialog load-more：本地 `runsCursor` state，load-more 传 cursor；刷新/搜索变更时清空 cursor。
- 测试：SessionList load-more 以 cursor 调用；RunsHistoryDialog load-more 以 cursor 调用 + 搜索重置 cursor。

## 5. YAGNI

不做：offset+cursor 同时传的校验（cursor 优先，文档说明）；seek 索引优化；时间线虚拟滚动；游标过期提示。

## 6. 验收清单

1. runs.md zh/en `?q=` 提及；CHANGELOG 目标搜索条目。
2. RunDetailDialog 时间线渲染 + 计数 + 空态（vitest）。
3. store/集成游标测试绿；无效 cursor 400。
4. 前端 tsc 0、npm run lint 0 违规、vitest 全绿。
5. 后端 go build/vet/test 全绿、make test-eval 34 场景 pass_rate 1。
6. spec 本节回写执行偏差。

## 7. 风险

- 行值比较 `(a < ? OR (a = ? AND id < ?))` 需 SQLite ≥3.15——modernc v1.60.1 内bundled 远新于此，实测验证。
- 前端 api 返回结构改动可能波及现有调用/测试——选择最小改动方案并在偏差记录。
- 时间线 15 个 i18n 键双语维护成本——键值一次性写入两字典。

## 8. 执行偏差

- 批1：EN 版 runs.md 的 curl `?q=` 示例首稿遗漏（zh 有 en 无），补提交 b83271f；grep 校验 1/1。
- 批2（store）：行值比较按计划用展开 OR 形式；`EncodeCursor`/`DecodeCursor` 导出（测试直接使用，非法 cursor 报错经 `fmt.Errorf` 包装）；store 老 offset 参数保留（cursor 非空时 offset 置 0，向后兼容）。
- 批2（handler）：**修复 R20 遗留真 bug**——sessions 列表 limit 参数从未解析（硬编码 50），文档承诺 1..200 未兑现；本批顺带修复（limit n>0 生效 + next_cursor 判断从硬编码 50 改为 limit）。cursor 解码失败 → 400 bad_request。
- 批3（时间线）：`timelineLabel = t('timeline.' + type)`，未知类型由 t() key 兜底回退为原始事件类型字符串；事件最多显示最近 50 条（计数仍为全量）；新增本地 relTime 副本（与 RunsHistoryDialog/MemoriesDialog 同模式，不跨组件抽象）。
- 批3（前端游标）：**初始加载也必须走 *Page 方法**——若首页仍用旧 listRuns/listSessions（无 next_cursor），第 2 页游标为空会重复拉取首页数据。因此 RunsHistoryDialog 的 load 效果与 session-store 的 loadSessions 均切换到 listRunsPage/listSessionsPage，旧数组版方法保留仅作兼容。loadMoreSessions 在 sessionsCursor 为空时 no-op（末页后按钮点击不重复请求）；RunsHistoryDialog 加载更多按钮条件追加 `&& runsCursor`。
- 测试适配：RunsHistoryDialog 8 个测试全量迁移到 listRunsPage mock（{items, nextCursor} 形状，断言改为 5 参 (undefined,50,status,q,cursor)）；session-store.test 的 listSessions mock → listSessionsPage；Header.test 的 listRuns → listRunsPage（Header 内 RunsHistoryDialog 联动）；SessionList load-more 断言 → loadMoreSessions(undefined)。
- 5 个既有 load-more/搜索测试断言随签名升级（vitest 186→192）。
