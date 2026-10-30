# Round 23 实施计划：next_cursor 文档补遗 + Run 全文搜索

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 4 个 API 文档补 cursor/next_cursor；CHANGELOG 补 R22 两条目；Run 搜索扩展为 goal + result_json 全文。

**Architecture:** 纯文档批零代码；搜索扩展仅改 task_repo.go 两处 q 块（同一参数绑定两次）+ i18n 占位符。

**Tech Stack:** Go 1.27 + modernc SQLite；React 18 + Vitest。无新依赖。

**Spec:** docs/superpowers/specs/2026-10-29-round23-cursordocs-fulltext-design.md（42b1b9a）

---

## Chunk 1

### Task 1: 文档补遗 + CHANGELOG

**Files:**
- Modify: `docs/api/runs.md` + `docs/en/api/runs.md`（参数表 +cursor 行；响应 +next_cursor 说明）
- Modify: `docs/api/sessions.md` + `docs/en/api/sessions.md`（同）
- Modify: `CHANGELOG.md`（Added +2 条）

- [ ] runs.md zh：参数表 `offset` 行后插

```markdown
| `cursor` | string | 否 | 游标翻页（上一页响应 `next_cursor` 的值）；非空时覆盖 `offset`；无效返回 400 |
```

响应节（`{"runs": [...]}` 说明处）加：`结果数等于 limit 时附 next_cursor（base64url），作为下一页 cursor 传入。`

- [ ] runs.md en 镜像（`| cursor | string | no | Cursor pagination (value of next_cursor from previous page); overrides offset when non-empty; invalid → 400 |` + 同义响应句）。
- [ ] sessions.md zh/en 同样处理（cursor 行 + next_cursor 句）。
- [ ] CHANGELOG Added 末尾（pprof 开关条目后）追加：

```markdown
- **游标分页**：sessions/runs 列表 `next_cursor` 响应与 `cursor` 参数（深翻页免 OFFSET 扫描，无效游标 400）；顺带修复 sessions limit 参数未解析的问题。
- **运行时间线**：运行详情弹窗事件时间线（14 类事件标签、`共 {n} 条事件` 计数、最近 50 条）。
```

- [ ] 验证：`grep -c cursor docs/api/sessions.md docs/api/runs.md docs/en/api/sessions.md docs/en/api/runs.md` 均 >0；`grep -c "游标分页\|运行时间线" CHANGELOG.md` = 2。
- [ ] Commit `docs(api): cursor pagination docs (zh/en) + changelog entries for cursor and timeline`。

### Task 2: Run 全文搜索（TDD）

**Files:**
- Modify: `backend/internal/store/task_repo.go`（两处 q 块）
- Test: `backend/internal/store/task_repo_test.go`（扩展 TestListRunsSearch）
- Test: `backend/tests/integration/runs_search_test.go`（追加 result 命中用例）
- Modify: `frontend/src/i18n/index.ts`（runs.searchPlaceholder ×2 字典）

- [ ] **Step 1 RED（store）**：TestListRunsSearch 追加子测试——mkRun 后 UpdateStatus 或 CreateRun 携带 ResultJSON（先看现有 helper 是否支持；若 setupRun 不带，测试内 `tasks.UpdateStatus(...)` 或直接改结构体字段后 CreateRun；以实际签名为准）。用例：`result-hit`（q 匹配 result_json 子串，goal 不含）命中；`goal-or-result`（q 命中 result 而 goal 不含 → 命中）；owner/session 两分支。
- [ ] **Step 2 跑测试确认失败**：`cd backend && go test ./internal/store/ -run TestListRunsSearch -count=1` → 期望 FAIL（result 不命中）。
- [ ] **Step 3 实现**：两处 q 块（约 L302、L373）替换为

```go
if term := strings.ToLower(strings.TrimSpace(q)); term != "" {
    query += ` AND (LOWER(goal) LIKE '%' || ? || '%' OR LOWER(COALESCE(result_json,'')) LIKE '%' || ? || '%')`
    args = append(args, term, term)
}
```

- [ ] **Step 4 GREEN**：store 测试通过。
- [ ] **Step 5 集成**：runs_search_test.go 追加——CreateRun 后通过 runtime 的 SetResult 路径或 store UpdateStatus 写 result_json，`GET /api/v1/runs?q=<result 子串>` 命中（先看 harness 暴露的 TaskRepo 方法与 UpdateStatus 签名；结果经 GET runs 或 GetRun 校验）。跑 `go test ./tests/integration/ -run TestRunGoalSearch -count=1`。
- [ ] **Step 6 i18n**：`runs.searchPlaceholder` zh `搜索目标或结果…` / en `Search goals or results…`。
- [ ] **Step 7 前端回归**：`cd frontend && npx tsc --noEmit && npm run lint && npx vitest run`（现有测试断言 q 透传与参数，不校验占位符文案 → 零改动预期）。
- [ ] **Step 8 Commit** `feat(store,http): run search matches goal or result_json (case-insensitive)`。

## Chunk 2

### Task 3: 全量验收 + spec 偏差回写

- [ ] 后端：`cd backend && go build ./... && go vet ./... && go test ./... -count=1 -timeout 600s && make test-eval`（期望 19 pkgs ok、evals 34 pass_rate 1）。
- [ ] 文档 grep 复核（Task 1 命令）。
- [ ] spec §7 偏差回写（执行中发现的真实偏差如实记录）。
- [ ] Commit `docs(spec): round 23 execution deviations` + `git status --short` 干净（.omo/ 除外）。

---

## 自查笔记（计划评审，无 subagent）

1. **UpdateStatus 签名未验证**：写 result_json 的具体入口（UpdateStatus 参数 or SetResult helper）在 Step 1/5 现场确认，计划不预设。
2. **q 块位置**：两处 q 块内容相同（L302/L373 附近），edit 时用足够上下文区分（第一个在 ListRunsBySession，第二个在 ListRunsByOwner；各自前面的 status 块一致——需带函数名级锚点或 replaceAll 评估）。
3. **前端零改动预期**：占位符文案变更不影响 q 透传测试；若 vitest 有快照断言文案则同步更新。
4. **COALESCE 必要**：runs 表 result_json 可为 NULL（未完成的 run）。
5. **性能**：LIKE 全扫已被 limit 钳制接受（spec §5），无索引需求。
