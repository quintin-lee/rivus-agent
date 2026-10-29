# Round 21 实施计划：CHANGELOG/overview 补遗 + Run goal 搜索 + evals 34

> **For agentic workers:** REQUIRED: Use superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 补 CHANGELOG 3 条 + overview 分页约定；为运行列表加 goal 搜索（store q → handler → 前端防抖框）；evals 32→34。

**Architecture:** 全镜像既有模式——R18 会话标题搜索（store LOWER LIKE 参数化 + handler 透传 + 前端 200ms 防抖 + useRef 跳挂载首跑）、R19 PostCheck-assert evals 场景。无新依赖。

**Tech Stack:** Go 1.27 + modernc SQLite；React 18 + Zustand + Vitest。

**Spec:** docs/superpowers/specs/2026-10-29-round21-changelog-runsearch-evals-design.md（d3b14b1）

---

### Task 1: CHANGELOG 3 条 + overview 分页小节（纯文档）

**Files:** `CHANGELOG.md`、`docs/api/overview.md`、`docs/en/api/overview.md`

- [ ] CHANGELOG [Unreleased] Added 在会话搜索条目后追加 3 条：MCP `tools/list_changed` 通知推送（SSE 长连接拉取，工具列表即时刷新，30 秒 Reconciler 兜底）；会话/运行列表 offset 分页（服务端 LIMIT/OFFSET 钳制 + 前端加载更多）；`AGENT_PPROF` 独立开关（未设置时跟随认证门控）。
- [ ] overview.md 在「基础约定」后加 `## 分页`：sessions（limit 缺省 50 上限 200）/ runs（limit 缺省 50 上限 100）支持 limit/offset（缺省 0），最新在前，越界空列表。en 镜像 `## Pagination`。
- [ ] 验证：grep CHANGELOG 三关键词各 ≥1；grep '^## 分页\|^## Pagination' zh/en 各 1。
- [ ] Commit `docs: changelog backfill (mcp push, pagination, pprof toggle) + pagination conventions (zh/en)`。

### Task 2: Run goal 搜索（TDD）

**Files:** `backend/internal/store/task_repo.go`、`backend/internal/api/http/handlers_runs.go`、`backend/cmd/agentd/export.go`、`backend/tests/bench/bench_test.go`、`backend/internal/store/task_repo_test.go`、`backend/tests/integration/runs_search_test.go`（新建）、`frontend/src/lib/api.ts`、`frontend/src/components/RunsHistoryDialog.tsx`、`frontend/src/i18n/index.ts`、`frontend/src/components/RunsHistoryDialog.test.tsx`

- [ ] Step 1 RED：store 单测 TestListRunsSearch（在既有 TestListRunsFilterByStatus 旁，setupRun 建 goal 'Alpha 任务'/'beta'，cases：空 q 全量 / 'alpha' 命中 / 'BETA' 命中小写 / miss 空）——编译失败（签名无 q）。
- [ ] Step 2 实现：两个 List 签名 status 后插 `q string`；非空 → `AND LOWER(goal) LIKE '%' || ? || '%'`（bind strings.ToLower(TrimSpace(q))）；callers（export.go `(...,"",100,0)`→`(...,"",…)` 按 R20 后实参插 q、bench_test.go 同）。
- [ ] Step 3 GREEN：store 包测试过；`go build ./...`。
- [ ] Step 4 handler：handleListRuns 两路径 `r.URL.Query().Get("q")` 透传；集成测试 runs_search_test.go（h.CreateRun 两个不同 goal → ?q= 过滤 / 无 q 全量 / miss 空）→ GREEN。
- [ ] Step 5 前端：api.listRuns 第 5 参 q（`&q=` encodeURIComponent，q 真值才拼）；RunsHistoryDialog 搜索框（searchDraft/query state + 200ms 防抖 + firstRun ref + query effect：`setRuns([]); setPage…` 直接 `listRuns(undefined, 50, statusFilter||undefined, 0, query||undefined)` 重拉第 1 页；loadMore 透传 query）；i18n `runs.searchPlaceholder` zh/en；测试（fake timers：输入 → advance 250ms → listRuns (undefined,50,undefined,0,'alpha')；清空 → (…,undefined)）。
- [ ] Step 6 验证：`go test ./internal/store/ ./tests/integration/ -count=1`；`cd frontend && npx tsc --noEmit && npx vitest run src/components/RunsHistoryDialog.test.tsx`。
- [ ] Commit `feat(store,http,frontend): run goal search via q param (case-insensitive LIKE)`。

### Task 3: evals 32→34

**Files:** `backend/tests/evals/scenarios_round21_test.go`（新建）、`backend/README.md:144`、`backend/README.en.md:148`

- [ ] TestScenarioSessionPagination：PostCheck 建 3 会话（sleep 2ms ×2）→ GET /sessions 全量 3 + ?offset=1 排除最新（2 条且不含最新）+ ?offset=99 空。
- [ ] TestScenarioRunGoalSearch：PostCheck 建会话 + CreateRun run-a（goal 含 marker-a）/ run-b（marker-b）→ GET /runs?q=marker-a 仅 run-a、无 q 含两者。
- [ ] README 计数 32→34（工具级 21 + agent 级 13）zh/en + scenarios_round21_test.go 入新建场景文件列表。
- [ ] 验证：`make test-eval` 34 pass_rate 1。
- [ ] Commit `test(evals): session pagination and run goal search scenarios (32 -> 34)`。

### Task 4: 全量验收 + spec 偏差回写

- [ ] `cd backend && go build ./... && go vet ./... && go test ./... -count=1 -timeout 600s && make test-eval`；`cd frontend && npx tsc --noEmit && npm run lint && npx vitest run`。
- [ ] spec §8 回写偏差；commit `docs(round21): spec deviations`；tree clean。

## 自查笔记

1. ListRuns 两函数现签名 (ctx, ownerID, sessionID|ownerID, status, limit, offset)——q 插在 status 后 offset 前，与 R18 sessions q 位置语义一致。
2. export.go/bench_test.go R20 已改 6/7 参，插 q 时逐点核对实参。
3. 前端防抖：复用 SessionList 的 firstRun ref + setTimeout 模式；RunsHistoryDialog 的 statusFilter 切换已有重拉逻辑，query effect 只管 q。
4. evals PostCheck 里 h.CreateRun 直接可用（R19 先例）。
