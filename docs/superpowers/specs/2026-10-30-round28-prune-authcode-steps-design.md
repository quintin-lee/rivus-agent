# Round 28 设计：运行数据保留 + MCP OAuth 授权码 + 步骤可视化

日期：2026-10-30 ｜ 状态：已批准（设计 'ok' m2884）

## 1. 背景

- `runs`/`run_steps`/`agent_events`/`approvals` 无限增长，除会话级联删除外无任何清理路径；长期运行是真实运维缺口。
- MCP OAuth 目前仅 client-credentials（服务对服务）；面向用户委托的服务器需要授权码流程。
- SystemStatusBubble 已接收 `steps` prop 但未渲染；气泡内有多处硬编码英文/中文混杂文案。

## 2. 批 1：运行数据保留（`agentd prune`）

### 2.1 Store

`TaskRepo.PruneRuns(ctx, cutoffMs int64) (int64, error)`：

1. `SELECT id, parent_run_id FROM runs WHERE status IN ('succeeded','failed','cancelled','timed_out') AND updated_at < ?`；
2. 并入子 run：`SELECT id FROM runs WHERE parent_run_id IN (第一阶段 ids)`（parent_run_id 无 FK，不删父会留孤儿）；
3. `DELETE FROM runs WHERE id IN (...)`（id 多时分批 ≤500/批）—— run_steps / agent_events / approvals 均有 `ON DELETE CASCADE`，随之清空；
4. 返回删除的 run 总数。memories 挂在 sessions 上，不受影响；active（queued/running/waiting_approval/paused）永不删除。

### 2.2 CLI

`agentd prune -older-than 720h [-dry-run] [-config ...]`：

- 复用 main.go dispatch 模式（`os.Args[1] == "prune"` → runPrune(os.Args[2:])，核心 pruneCore(args) error 可测）；
- `-older-than` duration 必填（缺失 exit 2）；`-dry-run` 打印将删除数量后返回，不执行删除；
- 输出 stderr 摘要（与 backup/export 一致）。无启动自动清理（YAGNI，运维定时调度）。

## 3. 批 2：MCP OAuth 授权码流程

`OAuthConfig` += `AuthorizeURL string json:"authorize_url,omitempty"` + `RedirectPort int json:"redirect_port,omitempty"`（0 → 默认 8099）。

流程（httpTransport.ensureToken 扩展）：

1. 配置了 authorize_url 且无有效 token → 启动 `127.0.0.1:RedirectPort` 临时 HTTP 监听；
2. 日志输出授权 URL（`authorize_url?client_id&redirect_uri=http://127.0.0.1:port/callback&state=<随机>&scope=...`，state 随机防 CSRF）；
3. 等待 `GET /callback?code&state`（state 校验，≤5 分钟超时）；
4. code 换 token（POST TokenURL，`grant_type=authorization_code` + redirect_uri）；
5. **access/refresh token 持久化**：经 `settings` KV（key `mcp.oauth.<server>`，JSON {access_token, refresh_token, expires_at}），重启免重授权；加载时仍在有效期直接复用；
6. 过期刷新 `grant_type=refresh_token`（有 refresh_token 时优先于 client_credentials）；401 沿用现有 invalidate + retry-once。

校验：stdio + authorize_url → 拒绝（沿 stdio+oauth 拒绝模式）；redirect_port 缺省 8099。

已知限制（设计已确认）：回环回调要求操作者浏览器可达服务器主机；远程无头部署建议改用设备码流程（本批 YAGNI）。

传输层持久化钩子：`httpTransport` 增加 `tokenStore`（Get/Set string 接口，app 侧用 SettingsRepo 适配）；无 store（单测直连）时跳过持久化。

## 4. 批 3：步骤可视化 + 气泡 i18n

- SystemStatusBubble 渲染 steps（steps.length > 0 时）：进度行 `步骤 {done}/{total} 已完成`（done = succeeded+failed+skipped）+ 每步行：状态点（running `animate-pulse`、failed 玫红、succeeded 绿、awaiting_approval 琥珀、其余灰）+ description；无步骤时不变。
- i18n 迁移气泡硬编码：`Cancel this run?`/`The run will stop...`/`Keep running`/`Cancel run`/`Cancel failed`/`重新运行` → t() 键（run.* 前缀），中英字典。

## 5. YAGNI

不做：启动自动清理、按会话保留策略、设备码流程、OAuth token 加密存储、步骤时间线拖拽/筛选、PRAGMA 清理空间回收（VACUUM 留给 backup）。

## 6. 验收清单

1. `agentd prune -older-than 0s -dry-run` 真机冒烟：打印计数、0 删除；实际执行后 runs/events/steps/approvals 同步减少。
2. PruneRuns 单测：旧终态删、新终态留、active 留、父子链同删、memories 保留。
3. auth-code 单测：回调交换全链路（authorize state 校验、code 换 token、持久化 roundtrip、refresh 优先）。
4. 前端：步骤渲染/计数/失败高亮测试；tsc 0、lint 0、vitest 全绿。
5. 后端 build/vet/test 全绿、evals 40 pass_rate 1。
6. spec 本节回写执行偏差。

## 7. 风险

- prune 的 `IN (...)` 列表过大：分批（每批 ≤500）执行 DELETE。
- 回调监听占用端口冲突：绑定失败 → 明确报错退出（不自动换端口，避免 URL 与监听不一致）。

## 8. 执行偏差

- 批1：runs 表列名为 `budget_json` 而非 `budget`（种子 INSERT 首版踩坑）；`CountPrunable` 按导出方法约定带 doc comment（golangci 要求）；SMOKE 用 `agentd export -session nosuch` 一次性建 schema 后再用 python 种子。
- 批2：`TokenStore` 接口签名与 `*store.SettingsRepo` 的 Get/Set 完全一致——app 直接接线，无需适配器；回调服务器将授权 URL 打到 stderr；auth-code 测试对 TIME_WAIT 绑定竞态做了 ×5 重试（100ms 早失败探测）；`WriteLine` 401 失效时同步清除持久化 token（`clearStoredToken`）；`HTTPTransportFactory` 增加 `TokenStore` 字段而非改 `newHTTPTransport` 签名。
- 批3：步骤列表区块本就存在——本批是加「步骤 x/y 已完成」计数头与包裹 div，非新建区块；既有 cancel 按钮测试断言由英文迁为中文（验证 i18n 行为）；`正在输入` aria-label 一并迁移（既有 zh 硬编码）；进度行（工具/模型）保持既有硬编码，超出本批范围；`StepStatus` import 未用移除（TS6196）。
- 工作树中 `.gitignore` 有外来未提交改动（openviking），本批未触碰、未提交。
