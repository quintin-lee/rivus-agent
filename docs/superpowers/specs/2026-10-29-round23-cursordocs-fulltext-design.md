# Round 23 设计：next_cursor 文档补遗 + Run 全文搜索

日期：2026-10-29 ｜ 状态：已批准（设计 'ok' m2650）

## 1. 背景

R22 上线游标分页（handlers 返回 `next_cursor`、`cursor` 参数、无效 400），但 sessions.md / runs.md（zh/en）4 个 API 文档 0 提及 `next_cursor`/`cursor`。CHANGELOG 缺 R22 两条目（游标分页、运行时间线）。

Run 搜索现仅匹配 goal；`result_json`（最大 50KB，受 MaxOutputBytes 钳制）未纳入——用户无法按"run 产出过什么"检索。

## 2. 批 1：文档补遗 + CHANGELOG（纯文档）

- docs/api/runs.md 与 docs/en/api/runs.md：GET /runs 参数表加 `cursor` 行（游标翻页，非空时覆盖 `offset`；格式 base64url，无效返回 400 bad_request）；响应说明加 `next_cursor` 字段（结果数等于 limit 时返回，用作下一页请求的 `cursor`）
- docs/api/sessions.md 与 docs/en/api/sessions.md：GET /sessions 同样补 `cursor` 参数行 + `next_cursor` 响应说明
- CHANGELOG.md [Unreleased] Added 追加：
  - **游标分页**：sessions/runs 列表 `next_cursor` 响应与 `cursor` 参数（深翻页免 OFFSET 扫描，无效游标 400）；顺带修复 sessions limit 参数未解析的问题
  - **运行时间线**：运行详情弹窗事件时间线（14 类事件标签、`共 {n} 条事件` 计数、最近 50 条）

## 3. 批 2：Run 全文搜索（goal + result_json）

### 3.1 后端

`ListRunsBySession` / `ListRunsByOwner`（task_repo.go）q 块扩展：

```sql
AND (LOWER(goal) LIKE '%' || ? || '%'
  OR LOWER(COALESCE(result_json,'')) LIKE '%' || ? || '%')
```

同一 term 绑定两次；`COALESCE` 兜 result_json 为 NULL 的运行。空 q 行为不变。

### 3.2 前端

仅 i18n 占位符更新：`runs.searchPlaceholder` zh `搜索目标…` → `搜索目标或结果…`；en `Search goals…` → `Search goals or results…`。RunsHistoryDialog 无代码改动（q 透传自动生效），测试无改动。

### 3.3 测试

- store 单测（扩展 TestListRunsSearch 或新增）：result_json 命中 / goal 命中 / goal 与 result 任一命中（或语义）/ 无命中
- 集成测试：创建带 result_json 的 run（`SetResult` 或直接 CreateRun 携带），`?q=<result 子串>` 命中

## 4. YAGNI

不做：FTS5 虚表、session 内容搜索（sessions 无正文）、搜索结果高亮、跨字段权重。

## 5. 风险

- result_json LIKE 全扫：单实例 SQLite + 已有 limit 钳制，进程内可接受；记录为已知行为，量大后再上 FTS5。

## 6. 验收清单

1. 4 个 API 文档 cursor/next_cursor 提及 >0（grep 验证）。
2. CHANGELOG 2 条新目。
3. store/集成全文搜索测试绿。
4. 后端 go build/vet/test 全绿、make test-eval 34 pass_rate 1。
5. 前端 tsc 0、lint 0、vitest 全绿（占位符变更不破坏现有测试——现测试断言 q 透传，不校验占位符文案）。
6. spec 本节回写执行偏差（如有）。

## 7. 执行偏差

（执行后回写）
