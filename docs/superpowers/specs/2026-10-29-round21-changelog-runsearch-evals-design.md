# Round 21 设计：CHANGELOG/overview 补遗 + Run goal 搜索 + evals 34

日期：2026-10-29 ｜ 状态：已批准（'4' 全套 m2444，设计 'ok' m2446）

## 1. 背景

CHANGELOG [Unreleased] 停在 R18 补遗，缺 R19 MCP 通知推送与 R20 分页/AGENT_PPROF。docs/api/overview.md 无分页横切约定（limit/offset 现横跨 sessions/runs）。Run goal 无搜索（仅状态过滤；会话标题搜索 R18 已有可镜像）。evals 32。

## 2. 批 1：CHANGELOG 同步 + overview 分页约定（纯文档）

CHANGELOG [Unreleased] Added 补 3 条：MCP `tools/list_changed` 通知推送（SSE 长连接拉取 + 工具列表即时刷新）；会话/运行列表 offset 分页（服务端 LIMIT/OFFSET 钳制 + 前端加载更多）；`AGENT_PPROF` 独立开关（未设置时跟随认证门控）。

overview.md zh/en 新增 `## 分页`：`GET /sessions`（limit 缺省 50、上限 200）与 `GET /runs`（limit 缺省 50、上限 100）支持 `limit`/`offset`（缺省 0），结果最新在前；越界 offset 返回空列表。runs.md/sessions.md 参数行已有，不重复。

## 3. 批 2：Run goal 搜索（镜像 R18 会话标题搜索）

- Store：`ListRunsBySession(ctx, ownerID, sessionID, status, q, limit, offset)` 与 `ListRunsByOwner(ctx, ownerID, status, q, limit, offset)` — q trim 非空追加 `AND LOWER(goal) LIKE '%' || ? || '%'`；空 q 字节等价。callers（export.go、bench_test.go）同步。
- Handler：`handleListRuns` 两条路径读 `q` 透传。
- 集成测试：goal 命中 / 大小写不敏感 / 无 q 全量 / 不命中空列表。
- 前端：`api.listRuns(sessionId?, limit=50, status?, offset=0, q?)` 追加第 5 参；RunsHistoryDialog 顶部搜索框（200ms 防抖，useRef 跳过挂载首跑——同 SessionList 模式），query 变化回到第 1 页带 q 重拉，loadMore 保留；i18n `runs.searchPlaceholder`（zh 搜索目标…/en Search goals…）。
- 前端测试：输入防抖后 listRuns 以 (undefined, 50, undefined, 0, 'q') 调用；清空恢复。

## 4. 批 3：evals 32→34

`scenarios_round21_test.go`（PostCheck-assert 风格，Run(t, Scenario{...})）：
- `TestScenarioSessionPagination`：建 3 会话（同 ms tie 需 sleep 2ms）→ GET /sessions?offset=1 排除最新、越界 offset 空列表、无 offset 全量。
- `TestScenarioRunGoalSearch`：建会话 + 2 个不同 goal 的 run → GET /runs?q= 命中过滤、无 q 全量。

README 计数 32→34（工具级 21 + agent 级 13）zh/en。

## 5. YAGNI

不做：runs 全文（含 result）搜索、FTS 索引、游标分页、搜索历史、LIKE 通配符转义（参数化已防注入）。

## 6. 验收清单

1. CHANGELOG 3 条新条目命中（分页/pprof/推送）。
2. overview.md zh/en 有 `## 分页` 小节。
3. `GET /runs?q=` 集成测试绿；前端防抖测试绿。
4. make test-eval 34 场景 pass_rate 1；README 计数 zh/en = 34。
5. 后端 go build/vet/test 全绿；前端 tsc 0 + lint 0 违规 + vitest 全绿。
6. spec §8 回写执行偏差。

## 7. 风险

- listRuns 签名已 5 参，再增长应改 options 对象——本次仍追加（调用点仅 2 处），记录为技术债。
- runs 两表钳制不同（sessions ≤200 / runs ≤100），overview 按端点分别标注，不统一（避免行为变更）。

## 8. 执行偏差

（执行后回写）
