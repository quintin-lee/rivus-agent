# Round 20 实施计划：分页 / pprof 开关 / MCP SSE

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** offset 分页（sessions/runs）、AGENT_PPROF 独立开关 + exhaustive-deps 升 error、MCP http 型 transport 的 GET SSE 推送通道。

**Architecture:** store 三方法加 offset（LIMIT ? OFFSET ?）；Pprof *bool nil=follow-auth；httpTransport 行队列合并 POST 响应与 GET SSE 通知行，复用 R19 交错通知解析。

**Tech Stack:** Go 1.27 + modernc SQLite；React 18 + Zustand；无新依赖。

**Spec:** docs/superpowers/specs/2026-10-29-round20-pagination-pprof-sse-design.md (cecd722)

---

## 批 1：分页

### Task 1: store offset（TDD）

**Files:** Modify backend/internal/store/task_repo.go（ListSessions :106 签名 `(ctx, ownerID, q string, limit, offset int)` 新增 limit 钳制 ≤0→50 >200→200；ListRunsBySession :237 / ListRunsByOwner :295 加 `offset int`，LIMIT 已有加 OFFSET）；Test task_repo_test.go 追加 TestListSessionsOffset（建 5 会话，limit=2 offset=0/2/4→2/2/1/空）+ 现有 runs 测试调用点补 0。

- [ ] RED：`go test ./internal/store/ -run TestListSessionsOffset` 编译错（参数不符）
- [ ] 实现（QueryContext args 追加 limit, offset）
- [ ] GREEN + `go build ./... && go vet ./...`（修全部调用点编译错：handlers_sessions.go:86、handlers_runs.go 两处、tests 中调用点传 0/50）
- [ ] Commit `feat(store): offset pagination for sessions and runs lists`

### Task 2: handler offset 参数 + 集成测试（TDD）

**Files:** handlers_sessions.go handleListSessions（`offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))`，负→0）、handlers_runs.go handleListRuns 同（两条路径都传）；sessions 段 limit 常量用 50；集成 tests/integration/session_pagination_test.go：3 会话 `?offset=1` 返回 2 条且不含第一页首条。

- [ ] RED（404/参数无效）→ 实现 → GREEN → Commit `feat(http): offset query param on sessions and runs listing`

### Task 3: 前端加载更多（TDD）

**Files:** api.ts listSessions(q, offset=0) / listRuns(sessionId?, limit=50, status?, offset=0)（URL 拼 &offset=）；session-store loadSessions(q, offset=0)（offset>0 时 append 而非替换，activeSessionId 逻辑仅 offset==0 时重算）；SessionList.tsx 底部 `sessions.length % 50 === 0 && sessions.length > 0` 时「加载更多」按钮（loadMore state 记 nextOffset，点击 loadSessions(query||undefined, nextOffset)）；RunsHistoryDialog.tsx 同（limit 50）；i18n common.loadMore zh 加载更多/en Load more；测试 SessionList.test.tsx + RunsHistoryDialog.test.tsx 各 1（mock 返回满页 → 按钮渲染 → 点击断言第二次调用带 offset）。

- [ ] RED → 实现 → `npx tsc --noEmit && npm run lint && npx vitest run` → Commit `feat(frontend): load-more pagination for sessions and run history`

## 批 2：pprof 开关 + lint

### Task 4: AGENT_PPROF（TDD）

**Files:** config.go `Pprof *bool` yaml `pprof` + applyEnv AGENT_PPROF（strconv.ParseBool，非法忽略保持 nil）；server.go:136 三分支（nil→AuthToken!=""，否则 *cfg.Pprof）；config_test.go TestPprofConfig（nil/true/false/非法）；server_security_test.go 或新 server_pprof_test.go：config.Config{AuthToken:"t", Pprof: boolPtr(false)} → GET /debug/pprof/ 404；{Pprof: boolPtr(true)}（无 token）→ 401。文档：.env.example、README zh/en 配置表、system.md pprof 段补 AGENT_PPROF。

- [ ] RED → 实现 → GREEN → Commit `feat(config): AGENT_PPROF independent pprof toggle (nil follows auth)`

### Task 5: exhaustive-deps error

**Files:** frontend/eslint.config.js 'warn'→'error'；若有违规修掉（预期现有代码干净，R15 已清）。

- [ ] `npm run lint` 0 违规 → Commit `chore(frontend): promote react-hooks exhaustive-deps to error`

## 批 3：MCP SSE 推送

### Task 6: httpTransport SSE 通知流（TDD）

**Files:** backend/internal/mcp/http.go——结构改造：httpTransport 加 `lines chan string`（cap 64）+ `stopped atomic.Bool` + `stopOnce`；NewHTTPTransport 启动 reader goroutine（GET url, Accept: text/event-stream，循环 bufio.Scanner：行以 "data:" 开头→TrimPrefix+TrimSpace 非空则 select 推入 lines；流结束且未 stopped→sleep 5s 重连）；WriteLine：POST（现逻辑）→ 响应体按现有解析（JSON 或 SSE 首个 data）→ 推入 lines；ReadLine：`<-t.lines`（stopped 且队列空→error "transport stopped"）；Stop：stopped.Store(true) + cancel GET + 关 resp + close(lines)（sync.Once 防双关）。httpCallTimeout 仍 35s 只管 POST。测试 http_sse_test.go：httptest.Server——POST handler 返回 JSON-RPC 响应；GET handler 写 2 条 SSE 通知（`data: {"jsonrpc":"2.0","method":"notifications/tools/list_changed","params":{}}\n\n`）后阻塞；transport 收集通知（直接读 lines 或配 Client.SetNotificationHandler）；断言 2 条通知 + 一次 Call 正常 + Stop 后 ReadLine 返回错误。

- [ ] RED（新测试编译/断言失败）→ 实现 → mcp 全包 GREEN → Commit `feat(mcp): GET SSE notification stream merged into http transport line queue`

### Task 7: 验收 + 文档 + 偏差回写

- [ ] `cd backend && go build ./... && go vet ./... && go test ./... -count=1 -timeout 600s && make test-eval`（32/32）
- [ ] frontend tsc/lint/vitest 全绿
- [ ] docs：docs/api/runs.md + en `offset` 行；backend README zh/en pprof 句；spec §6 偏差回写 → Commit `docs(round20): pagination params, pprof toggle, sse push notes (zh/en) + spec deviations`

---

## 自查笔记

1. ListSessions 现签名 `(ctx, ownerID, q)`——Task 1 改 5 参，唯一生产调用点 handleListSessions；tests/integration/session_search_test.go 不直接调 store，无影响。
2. offset 负数：handler 层钳 0；store 层 `offset < 0 → 0` 双保险。
3. SessionList 满页判定 `len % 50 === 0`：后端 limit 钳 200、handler 传 50 —— 一致。
4. httpTransport 现构造（R12）：WriteLine POST+解析在函数内、ReadLine 返回缓存响应？——执行时先读 http.go 现结构再动手，若 WriteLine/ReadLine 是配对缓存模式，改造为队列模式时保持 NewHTTPTransport 签名与 Stop 语义不变；既有 9 个 http_test 用例必须原样通过。
5. atomic import 路径 sync/atomic；bufio.Scanner buffer 上限 1MB 沿用 http.go 现值。
6. 分页集成测试注意 FK：CreateSession 即可（sessions 无 FK 依赖）。
