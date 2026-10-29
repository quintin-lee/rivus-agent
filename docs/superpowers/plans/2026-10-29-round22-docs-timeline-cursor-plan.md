# Round 22 实施计划：文档补遗 + Run 时间线 + 游标分页

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 补 runs.md q 参数文档与 CHANGELOG 条目；RunDetailDialog 增加事件时间线区块；sessions/runs 列表在 offset 旁并存游标分页（`next_cursor` 响应字段 + 前端 load-more 切游标）。

**Architecture:** 文档纯文本；时间线复用 RunDetailDialog 已拉取的 events，垂直节点 + i18n 标签映射；游标 = base64url(`sortValue:id`)，SQL 行值比较 `AND (sort < ? OR (sort = ? AND id < ?))`，cursor 非空时忽略 offset；前端经新增 `*Page` api 方法取 `{items, nextCursor}`，避免波及现有数组返回类型的调用方与测试。

**Tech Stack:** Go 1.27 + modernc SQLite（行值比较需 ≥3.15，实测验证）；React 18 + Zustand + Vitest。无新依赖。

**Spec:** docs/superpowers/specs/2026-10-29-round22-docs-timeline-cursor-design.md（302cb6f）

---

## Chunk 1

### Task 1: 文档补遗

**Files:** Modify `docs/api/runs.md` / `docs/en/api/runs.md`（GET /api/v1/runs 参数表）/ `CHANGELOG.md`

- [ ] runs.md zh：参数表 `offset` 行后加 `| q | string | 否 | 按目标（goal）大小写不敏感子串过滤，省略=不过滤 |`；curl 示例区（如有）加 `curl 'localhost:8080/api/v1/runs?q=alpha'`；en 镜像。
- [ ] CHANGELOG 会话搜索条目后加：`- **运行目标搜索**：GET /api/v1/runs?q=（goal 大小写不敏感子串过滤）+ 前端运行历史搜索框（200ms 防抖）。`
- [ ] 验证：`grep -c '?q=' docs/api/runs.md docs/en/api/runs.md` ≥1/≥1；`grep -c 目标搜索 CHANGELOG.md` ≥1。
- [ ] Commit `docs(api): runs list q param docs and changelog entry (zh/en)`。

### Task 2: store 游标（TDD）

**Files:** Modify `backend/internal/store/task_repo.go`（cursor helpers + 3 签名）/ `backend/internal/store/task_repo_test.go`

- [ ] RED：追加 TestSessionsCursorPagination（mkSession 用 CreateSession + 2ms sleep ×4）与 TestRunsCursorPagination（沿用 TestListRunsSearch 的 mkRun 目标）：
  - 全量 ListSessions 顺序 = [s4,s3,s2,s1]（updated_at DESC）；`encodeCursor` 从末行起：page1 = limit 2 + cursor(from s3) → 得 [s2,s1]；空 cursor 首页 [s4,s3]；跨两页无重叠无遗漏；`decodeCursor("!!!bad")` 返回 error；`decodeCursor(encodeCursor(...))` 还原。
  - 签名改动先行写入测试（多参调用），编译失败即 RED。
- [ ] 实现：
  ```go
  func encodeCursor(sortValue int64, id string) string {
      return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d:%s", sortValue, id)))
  }
  func decodeCursor(s string) (int64, string, error) {
      raw, err := base64.RawURLEncoding.DecodeString(s)
      if err != nil { return 0, "", err }
      i := strings.IndexByte(string(raw), ':')
      if i <= 0 { return 0, "", fmt.Errorf("malformed cursor") }
      v, err := strconv.ParseInt(string(raw)[:i], 10, 64)
      if err != nil { return 0, "", err }
      return v, string(raw)[i+1:], nil
  }
  ```
  （imports += encoding/base64, fmt, strconv——fmt/strconv 需核对是否已 import。）
- [ ] 三个列表函数加 `cursor string` 尾参；函数体开头：
  ```go
  if cursor != "" {
      sortVal, lastID, err := decodeCursor(cursor)
      if err != nil { return nil, fmt.Errorf("invalid cursor: %w", err) }
      offset = 0
      query += ` AND (updated_at < ? OR (updated_at = ? AND id < ?))` // runs 用 created_at
      args = append(args, sortVal, sortVal, lastID)
  }
  ```
  注意：LIKE 段在 cursor 段之前追加，保持 SQL 合法（WHERE … AND … AND …）；`LIMIT ? OFFSET ?` 已在尾部。
- [ ] 调用点修复（grep `ListSessions(`、`ListRunsBy`）：export.go `ListRunsBySession(...,100,0,"")`；bench_test.go `ListRunsByOwner(ctx,"u1","","",50,0,"")`；task_repo_test.go 全部既有调用 +`""`；integration 既有调用如有。
- [ ] `go test ./internal/store/ ./tests/integration/ -count=1` 绿。
- [ ] Commit `feat(store): cursor pagination for sessions and runs (base64url of sort:id)`。

### Task 3: handler next_cursor + 400（TDD）

**Files:** Modify `backend/internal/api/http/handlers_sessions.go` / `handlers_runs.go` / Test `backend/tests/integration/cursor_pagination_test.go`（新建）

- [ ] RED：TestCursorPaginationAndNext —— 建 3 会话（2ms sleep）：GET /api/v1/sessions?limit=2 → body 含 `"next_cursor"`；用其 GET ?limit=2&cursor=… → 返回第 2 页（无首页 id）；GET ?cursor=bad!! → 400 + `"bad_request"`。
- [ ] 实现：handlers 读 `cursor := r.URL.Query().Get("cursor")`；响应组装：
  ```go
  resp := map[string]any{"sessions": sessions}
  if len(sessions) == limit && limit > 0 && len(sessions) > 0 {
      last := sessions[len(sessions)-1]
      resp["next_cursor"] = store.EncodeCursor(last.UpdatedAt, last.ID)
  }
  writeJSON(w, 200, resp)
  ```
  需导出 `store.EncodeCursor`（EncodeCursor = encodeCursor 别名导出；decodeCursor 保持私有，handler 经 store 返回 error 判 400）。
  decode 失败：`if _, _, err := store.DecodeCursor(cursor); err != nil { writeErr(400 bad_request) }`（DecodeCursor 导出）。
  handleListRuns 两路径同样处理（last 行取 CreatedAt + ID）。
- [ ] 集成测试绿 + 全 store/回归绿。
- [ ] Commit `feat(http): cursor param with next_cursor response on sessions and runs`。

## Chunk 2

### Task 4: RunDetailDialog 时间线（TDD）

**Files:** Modify `frontend/src/components/RunDetailDialog.tsx` / `frontend/src/i18n/index.ts` / Test `frontend/src/components/RunDetailDialog.test.tsx`

- [ ] RED：追加 2 测试（沿用现有 mock——getRun/getEvents/listApprovals 已 mock；现 mock events 数组扩展几个事件）：
  1. 渲染时间线：给 getEvents mock 返回 [{seq,type:'run.started',...},{type:'tool.completed'},{type:'budget.consumed'}] → 断言出现 `运行开始`、`工具完成`、`预算消耗` 标签与 `共 3 条事件` 计数行。
  2. 空事件 → `（无事件）` 空态 + 不渲染计数行。
- [ ] i18n（zh/en 两字典各 17 键）：`timeline.title` 时间线/Timeline、`timeline.eventCount` 共 {n} 条事件/{n} events、`timeline.empty` （无事件）/(no events)；映射键：run.started 运行开始/Run started、run.finished 运行完成/Run finished、run.failed 运行失败/Run failed、run.cancelled 已取消/Cancelled、run.timed_out 已超时/Timed out、model.completed 模型完成/Model completed、model.token 流式输出/Streaming、model.failed 模型失败/Model failed、tool.completed 工具完成/Tool completed、tool.failed 工具失败/Tool failed、approval.requested 等待审批/Approval requested、budget.consumed 预算消耗/Budget consumed、budget.exceeded 预算超限/Budget exceeded、verification.passed 校验通过/Verification passed、verification.failed 校验失败/Verification failed。
- [ ] 实现（RunDetailDialog）：`timelineLabel(type)` = 以 `timeline.${type}` 查字典、不存在回退原始 type（i18n dict 需暴露 has/或 t() 已按 key 回退——t 回退到 key 本身，英文 type 即原文，可接受并记录偏差）；`const shown = events.slice(-50)`；区块 JSX：竖线（`absolute left-1.5 top-0 bottom-0 w-px bg-border`）+ 每节点 `flex gap-2`（圆点 `h-3 w-3 rounded-full border` + label + relTime(ev.created_at)）；计数行 `t('timeline.eventCount').replace('{n}', String(events.length))`；events.length===0 → 空态。置于审批区块之后。
- [ ] `npx tsc --noEmit && npm run lint && npx vitest run src/components/RunDetailDialog.test.tsx`。
- [ ] Commit `feat(frontend): event timeline section in run detail dialog`。

### Task 5: 前端游标切换（TDD）

**Files:** Modify `frontend/src/lib/api.ts` / `frontend/src/store/session-store.ts` / `frontend/src/components/SessionList.tsx` / `frontend/src/components/RunsHistoryDialog.tsx` / 各测试

- [ ] RED：
  - api.test.ts：listSessionsPage 无 cursor → url `/api/v1/sessions?limit=50`、带 cursor → `&cursor=c1`；listRunsPage 同理；返回 `{items, nextCursor}` 解析。
  - SessionList.test.tsx：改既有 load-more 断言——50 会话点击按钮 → `loadMoreSessions` 被调（store mock 换名）。
  - RunsHistoryDialog.test.tsx：load-more 断言改 `listRunsPage` 调 `(undefined, 50, undefined, undefined, 'c0')`；搜索变更后 cursor 重置。
- [ ] 实现：
  ```ts
  async listSessionsPage(q?: string, cursor?: string): Promise<{ items: Session[]; nextCursor?: string }> {
    // path 构建同 listSessions（limit=50 固定），cursor 非空拼 &cursor=
    const data = await this.request<{ sessions: Session[]; next_cursor?: string }>('GET', path)
    return { items: data.sessions ?? [], nextCursor: data.next_cursor }
  }
  async listRunsPage(sessionId?: string, limit = 50, status?: string, q?: string, cursor?: string): Promise<{ items: Run[]; nextCursor?: string }> { …同上… }
  ```
  旧 listSessions/listRuns 保留不动（兼容既有调用与测试）。
  session-store：`sessionsCursor: string` state + `loadMoreSessions: () => Promise<void>`（调 listSessionsPage(get().当前搜索词?, get().sessionsCursor) —— 搜索词不在 store，简化为不带 q（与现 load-more 行为一致：现传 query||undefined 来自组件）；组件持 query，故 loadMoreSessions(q?: string)：`const { items, nextCursor } = await api.listSessionsPage(q || undefined, get().sessionsCursor || undefined); set(s => ({ sessions: s.offsetMode ? s.sessions : [...s.sessions, ...items], sessionsCursor: nextCursor ?? '' }))` —— offset 遗留模式删除，统一 cursor；loadSessions 全量重载置 sessionsCursor=''。
  SessionList：load-more onClick → `loadMoreSessions(query || undefined)`。
  RunsHistoryDialog：`const [runsCursor, setRunsCursor] = useState('')`；loadMore → `api.listRunsPage(undefined, 50, statusFilter || undefined, query || undefined, runsCursor || undefined)` append + setRunsCursor(nextCursor ?? '')；load effect 与 query/status 变化处置 runsCursor=''。
- [ ] `npx tsc --noEmit && npm run lint && npx vitest run` 全绿（28+ files）。
- [ ] Commit `feat(frontend): load-more switches to cursor pagination`。

### Task 6: 全量验收 + spec 偏差回写

- [ ] `cd backend && go build ./... && go vet ./... && go test ./... -count=1 -timeout 600s && make test-eval`（34 场景 pass_rate 1）。
- [ ] `cd frontend && npx tsc --noEmit && npm run lint && npx vitest run`。
- [ ] spec §8 偏差回写（timelineLabel 用 t() key 回退而非显式 has 检查；store 去掉 offset 遗留 append 模式等真实偏差）。
- [ ] Commit `docs(round22): execution deviations`；`git status --short` 干净（.omo/ 除外）。

---

## 自查笔记

1. **行值比较**：`(updated_at < ? OR (updated_at = ? AND id < ?))` 避免依赖 SQLite row-values 语法（等价展开，3.15 前也兼容）——比纯行值 `(updated_at, id) < (?, ?)` 更稳。
2. **next_cursor 导出**：handler 侧需要 encode → 导出 `store.EncodeCursor/DecodeCursor`；decodeCursor 私有保留亦可（handler 只用 DecodeCursor 校验）。
3. **cursor 优先于 offset**：同时传时 cursor 生效，文档写明，不做互斥校验（spec §5）。
4. **%50 按钮条件保留**：cursor 模式下满页仍等价 50 行（limit 固定 50），现有显隐测试不破。
5. **listRunsPage 参数序**（sessionId, limit, status, q, cursor）与既有 listRuns（…, offset, q）不同——新方法独立无冲突，偏差记录。
6. **时间线 count 行**：t() 无 {n} 插值则用 replace；确认 t() 已支持 {name} 占位（R13 支持 `{name}`），键值用 `{n}` 并以 replace('{n}', …) 处理（t 的占位实现按 {name} 硬编码——执行时核对，必要时 replace）。
7. **既有 listSessions 测试**（api.test.ts offset 断言）不受影响——旧方法保留。
