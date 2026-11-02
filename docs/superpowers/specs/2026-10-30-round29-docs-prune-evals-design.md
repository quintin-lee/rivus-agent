# Round 29 设计：文档补遗（auth-code / CHANGELOG）+ prune 评估场景

日期：2026-10-30 ｜ 状态：待批准（设计 'ok' 已确认执行）

## 1. 背景

R28 新增 MCP OAuth 授权码配置（`authorize_url` / `redirect_port`）但未同步 API 文档；
CHANGELOG `[Unreleased]` 停在 R25（复制按钮 / 聊天空状态），R26-R28 的 运行保留（prune）、
OAuth 授权码、步骤可视化 均无条目；prune 路径（`agentd prune`）是 CLI 不进 HTTP surface，
无评估场景覆盖。

## 2. 批1：文档补遗（纯文档）

### 2.1 docs/api/settings.md（zh/en）

- 行 36 `mcpServers[].oauth` 字段行：补 `authorize_url?` + `redirect_port?` 两字段，
  说明「authorize_url 非空时走授权码流程，redirect_port 必须 >0；stdio 服务器携带
  authorize_url 拒绝」。
- 行 18 `mcp.servers[].oauth`（GET 回显行）：echo 字段补 `authorize_url?` / `redirect_port?`，
  client_secret 永不回显的说明保留。

### 2.2 CHANGELOG.md `[Unreleased]`

追加 R26-R28 条目（Added）：

- **运行保留**：`agentd prune -older-than <d> [-dry-run]` 清理过期终态 run
  及其级联 events/steps（`-dry-run` 只报数不删）。
- **MCP OAuth 授权码**：authorize_url 非空时走本地回调 + 授权码交换，
  token 持久化（`SettingsRepo`）；client-credentials 与授权码双模式。
- **步骤可视化**：状态气泡「步骤 x/y 已完成」进度头 + 取消对话框 i18n。

## 3. 批2：evals 40→42（prune 场景，TDD）

`agentd prune` 是 CLI 不进 HTTP，评估走 store 直调：

- **TestScenarioPruneRemovesOldTerminal**：种 1 个过期终态 run（backdate `updated_at`）
  + 1 个 running run → `PruneRuns(ctx, cutoffMs)` 后过期 run 消失
  （agent_events / run_steps 级联归零），running 存活。
- **TestScenarioPruneDryRunNoDeletion**：`CountPrunable` >0 但传入 `dryRun=true`
  分支（不执行 DELETE）后行数不变。

## 4. 批3：全量验收 + spec 偏差回写 + wrap-up

- 后端 `go build/vet/test`（19 pkgs）+ `make test-eval`（42 场景 pass_rate 1）。
- 前端 `npx tsc --noEmit` + `npm run lint` + `npx vitest run`（本轮零前端改动，仅回归）。
- 文档 grep 验证：settings.md `authorize_url` 命中 2（zh/en）、CHANGELOG 3 条新增。
- spec §6 执行偏差回写。

## 5. YAGNI

不做：S3 接线落地（.env.example S3 行）、MCP auth-code UI 流程、prune 走 HTTP surface。

## 6. 验收清单

1. settings.md zh/en oauth 字段行含 `authorize_url` / `redirect_port`（grep 命中 2/2）。
2. CHANGELOG `[Unreleased]` Added 含 prune / 授权码 / 步骤可视化 3 条。
3. 新增 2 个 prune 场景 PASS；`make test-eval` total=42 pass_rate=1。
4. 全量回归 19 pkgs 0 FAIL；前端 tsc 0 / lint 0 / vitest 全绿。
5. spec 执行偏差回写。
