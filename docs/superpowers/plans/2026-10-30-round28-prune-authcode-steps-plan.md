# Round 28 实施计划：数据保留 + auth-code + 步骤可视化

> **For agentic workers:** REQUIRED: Use superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `agentd prune` 数据保留 CLI、MCP OAuth 授权码流程（token 持久化）、活跃 run 步骤可视化与气泡 i18n。

**Architecture:** prune = 两阶段 SELECT→分批 DELETE（FK cascade）；auth-code = ensureToken 扩展（回环回调 + SettingsRepo 持久化钩子）；步骤 = SystemStatusBubble 渲染已有 steps prop。

**Tech Stack:** Go 1.27 / React 18 + Vitest。无新依赖。

**Spec:** docs/superpowers/specs/2026-10-30-round28-prune-authcode-steps-design.md（e7dd01c）

---

## Task 1: store.PruneRuns（TDD）

**Files:** Modify backend/internal/store/task_repo.go；Test backend/internal/store/task_repo_test.go

- [ ] RED：TestPruneRuns —— seed：旧终态 run_old（updated_at 手写 SQL 回拨或 CreateRun 后 UPDATE）、新终态 run_new、active run_act、父 run_p（旧终态）+ 子 run_c（parent_run_id=run_p）；各配 events/approvals 断言级联。断言返回数=2（old+p），且 run_old/run_p/run_c 及其 events/steps/approvals 消失，run_new/run_act 存在，memories 保留。updated_at 回拨用 `UPDATE runs SET updated_at=? WHERE id=?`（nowMs 毫秒可控）。
- [ ] GREEN：PruneRuns 实现（两阶段 + 分批 500）。
- [ ] Commit `feat(store): PruneRuns removes terminal runs older than cutoff with cascade`

## Task 2: agentd prune CLI（TDD + 冒烟）

**Files:** Create backend/cmd/agentd/prune.go；Modify main.go；Test backend/cmd/agentd/prune_test.go

- [ ] RED：prune_test.go —— pruneCore(['-older-than','0s']) 对 seed DB dry-run=false 返回 nil 且删除发生；缺 -older-than → errPruneUsage。
- [ ] GREEN：prune.go（errPruneUsage sentinel、-config/-older-than/-dry-run、store.Open、PruneRuns、stderr 摘要）+ main.go dispatch。
- [ ] 冒烟：make build → AGENT_DB_PATH 临时库 → prune -older-than 0s -dry-run 打印 N → 实际执行后 sqlite count 验证；kill 用精确 PID（勿 pkill）。
- [ ] Commit `feat(cli): agentd prune subcommand for run retention`

## Task 3: OAuth auth-code（TDD）

**Files:** Modify backend/internal/mcp/config.go、http.go、manager.go（tokenStore 接线）；Test backend/internal/mcp/http_oauth_test.go 扩展

- [ ] RED：TestHTTPOAuthAuthCodeFlow —— httptest：authorize 断言（不需要，回环由 transport 自起）+ token 端点断言 grant_type=authorization_code&code&redirect_uri；模拟回调：transport 起监听后测试侧 GET http://127.0.0.1:<port>/callback?code=abc&state=<捕获>；断言 ensureToken 完成、resource 调用带 Bearer。TestHTTPOAuthTokenPersistence —— tokenStore 假件（map），首次后 Set 被调；重建 transport 加载后不再走 token 端点。
- [ ] GREEN：OAuthConfig += authorize_url/redirect_port；ValidateSpecs stdio+authorize_url 拒绝；httpTransport += tokenStore TokenStore 接口（Get/Set）、authCode 状态机（ensureToken：持久化命中→用；authorize_url 配置→监听回调→exchange→持久化；有 refresh_token→refresh 优先；否则 client_credentials 原路）。manager.connect 传入 settings 适配器（app.go）。
- [ ] mcp 全包回归（SSE/pairing/oauth 旧测试不动）。
- [ ] Commit `feat(mcp): OAuth authorization-code flow with persisted tokens`

## Task 4: 步骤可视化 + 气泡 i18n（TDD）

**Files:** Modify frontend/src/components/Messages/SystemStatusBubble.tsx、frontend/src/i18n/index.ts；Test frontend/src/components/Messages/SystemStatusBubble.test.tsx

- [ ] RED：steps 3 条（succeeded/running/failed）→ 渲染 3 步描述 + '步骤 1/3 已完成' 断言（done=succeeded+failed+skipped=2？—— 以实现定义为准，测试先行定义：done=非 pending/running/awaiting_approval）；空 steps → 不渲染区块。
- [ ] GREEN：i18n run.* 键（zh/en 双字典：cancelTitle/cancelDesc/keepRunning/cancelRun/cancelFailed/rerun/stepsProgress {done}{total}）+ 步骤区块 + 现有硬编码迁移。
- [ ] tsc + lint + vitest 全量。
- [ ] Commit `feat(frontend): step progress visualization and bubble i18n`

## Task 5: 全量验收 + 偏差回写 + wrap-up

- [ ] backend build/vet/test + evals 40；frontend tsc/lint/vitest。
- [ ] spec §8 偏差回写 + commit。
- [ ] wrap-up 报告。

## 自查笔记

1. PruneRuns 的 updated_at 回拨用 SQL UPDATE（CreateRun 内部写 nowMs，不可注入）。
2. 回环测试端口：RedirectPort 显式传 0 → 由 transport 选空闲端口？——否，spec 定 0→默认 8099；测试传显式高位端口（如 18099 起，冲突重试由测试保证独占）。回调 URL 构造需与监听端口一致。
3. tokenStore 接口放 mcp 包内（Get(ctx,key)/Set(ctx,key,val)），app 侧适配 SettingsRepo；单测传 map 假件，nil store 跳过持久化（直连行为不变）。
4. SystemStatusBubble steps done 定义：终态（succeeded/failed/skipped）计入完成，pending/running/awaiting_approval 不计。
5. .gitignore 的 openviking 行是外部改动，任何 commit 不含它。
