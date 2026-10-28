# Round 14 设计：可观测性补全（pprof + Prometheus）· 评估与基准深化 · 前端体验收尾

日期：2026-10-28 ｜ 状态：已批准（design 'ok'）
分支：master 直提 ｜ 测试：TDD 先行 ｜ 子代理：不使用（用户约束）

## 1. 背景与问题

- `/metrics` 仅有 expvar JSON，Prometheus 抓取器无法直接消费；运行时性能剖析（CPU/堆/goroutine）无入口。
- evals 20 个场景中预算三维度只测了 tool/model 两个，`output_bytes_budget_exceeded` 无覆盖；skill references、取消中途运行也无场景。
- bench 仅 3 个（EventAppend/ToolExecute/APICreateRun），读路径、幂等缓存、预算门禁开销无基准。
- 前端 i18n 迁移（R13）残留 2 处 `'加载失败'` 硬编码；运行详情（steps/工具调用/审批）只能看聊天流，无聚合视图。

## 2. 结构

```
backend/internal/api/http/metrics_prometheus.go   新增：expvar→Prom 文本转换
backend/internal/api/http/routes.go               修改：+GET /metrics/prometheus；+pprof 条件注册
backend/internal/api/http/server.go               修改：Server 持有 pprofEnabled（AuthToken!=""）
backend/tests/evals/scenarios_round14_test.go     新增：4 个新场景
backend/tests/bench/bench_test.go                 修改：+5 个基准
frontend/src/i18n/index.ts                        修改：+common.loadFailed 等键
frontend/src/components/ToolsPanel.tsx            修改：硬编码→t()
frontend/src/components/SettingsDialog.tsx        修改：硬编码→t()
frontend/src/components/RunDetailDialog.tsx       新增：运行详情聚合视图
frontend/src/components/RunsHistoryDialog.tsx     修改：行点击→打开详情
frontend/src/lib/api.ts                           修改：+listEvents/listApprovals（若缺）
```

## 3. 批次

### 3.1 批1 可观测性补全
- **pprof**：`Server` 增加 `pprofEnabled = cfg.AuthToken != ""`；仅当启用时在 routes.go 注册 `/debug/pprof/`（`pprof.Index`，name 路径覆盖 heap/goroutine/allocs/block/mutex/threadcreate）与 `/debug/pprof/cmdline`、`/debug/pprof/profile`（CPU）、`/debug/pprof/symbol`、`/debug/pprof/trace`。auth 关闭（token 为空）= 本地开发 → 不注册（404），安全默认。auth 开启时全部在 auth 中间件之后（链：securityHeaders→requestID→requestLog→auth→rateLimit→mux），需 Bearer。
- **Prometheus**：`expvarPromHandler` 遍历 `expvar.Do`：顶层 Int/Float → `# TYPE <name> counter` + `<name> <value>`；`expvar.Map`（tool_calls_total/tool_errors_total）→ 每个 key 输出 `<name>{key="<k>"} <v>`；JSON 类型跳过（无已知实例）。Content-Type `text/plain; version=0.0.4; charset=utf-8`。路由 `GET /metrics/prometheus`，auth 生效时同样需 Bearer（与 /metrics 一致，不豁免）。
- 测试：server_obs_round14_test.go——(a) token 开启：无 token 访问 /debug/pprof/ → 401；带 token → 200 含 "goroutine" 索引页；/debug/pprof/heap?debug=1 带 token → 200 含 "Heap"。(b) token 空：/debug/pprof/ → 404（未注册）。(c) /metrics/prometheus 带 token → 包含 `tool_calls_total{key=` 与 `# TYPE` 行、`rate_limited_total`。(d) 无 token → 401。

### 3.2 批2 评估与基准深化
- 新场景（scenarios_round14_test.go，复用既有框架钩子）：
  1. `invoke_skill_reference_load`：NewToolConfigHarness + 临时 skill 目录（SKILL.md + references/cheatsheet.md），EnqueueToolCall invoke_skill `{"name":"demo","reference":"cheatsheet.md"}` → succeeded，tool.completed 存在（输出内容经 toolObs 进 payload，断言 payload 含 args+output）。
  2. `file_edit_read_roundtrip`：withTempWorkspace 写 seed 文件 → file_edit（conditional approval，MidRun 审批）→ SetFallback 重发 file_read → succeeded，PostCheck 文件内容断言。
  3. `budget_output_bytes_cap`：budget max_output_bytes 极小（如 10）+ EnqueueText 长文本 → failed + ErrorCode == output_bytes_budget_exceeded + EvtBudgetExceeded 存在。
  4. `cancel_running_run`：loopToolSlow（250ms/轮）+ MidRun POST cancel → status cancelled + EvtRunCancelled 存在。
- bench 追加：`BenchmarkExecutorIdempotentHit`（同 (runID,toolCallID) 重复执行命中缓存）、`BenchmarkBudgetOverBudget`（tracker 未超限检查开销）、`BenchmarkEventsListAfter`（预写 100 事件后 ListAfter 读）、`BenchmarkListRunsByOwner`（预写 20 顶层 run 读）、`BenchmarkSQLiteMixedRW`（b.Run 并行 8 goroutine 混合 Append+GetRun）。文件 DB（t.TempDir 风格 b.TempDir）。
- 验收：make test-eval 24 场景 pass_rate 1；go test ./bench -count=1 全绿（bench 仅编译运行冒烟，不锁数值）。

### 3.3 批3 前端体验收尾
- i18n 收尾：`common.loadFailed`（zh: 加载失败 / en: Failed to load）；ToolsPanel.tsx:34、SettingsDialog.tsx:41 → `t('common.loadFailed')`（ApiError 分支保留 e.message）。sse-client.ts 剩余命中为注释，不动（执行时核实）。
- **RunDetailDialog**：Props `{ runId: string | null, onClose: () => void }`；打开时并行拉 `GET /runs/{id}`（{run, steps}）+ `GET /runs/{id}/events?after=0&limit=200` + `GET /runs/{id}/approvals`。渲染：头部（statusLabel 徽章 + goal + error_code 红字）、预算用量（budgetUsage 键复用 status.ts）、steps 时间线（step_index + 名称 + 状态）、工具调用（tool.completed 事件且带 args/output 的，`<pre>` 截断展示）、审批（状态 + reason）。加载/错误重试/空态。文案 `rundetail.*` 键。
- 入口：RunsHistoryDialog 行点击 → 打开 RunDetailDialog（该弹窗内层级叠加）。api.ts 缺失则补 `listEvents(runId, after?, limit?)`、`listApprovals(runId)`（getRun 若已有则复用）。
- 测试：RunDetailDialog.test.tsx（渲染 steps/工具调用/审批、错误+重试、runId=null 不请求）；RunsHistoryDialog.test.tsx 行点击回调；i18n 键存在。

## 4. 验收（DoD）

1. `cd backend && go build ./... && go vet ./...` 通过。
2. `go test ./... -count=1 -timeout 600s` 全绿；`make test-eval` 24 场景 pass_rate 1。
3. `go test ./tests/bench -run '^$' -bench . -benchtime 1x` 可运行（冒烟）。
4. 前端 `npx tsc --noEmit` 0 错误 + `npm run lint` 通过 + `npx vitest run` 全绿。
5. pprof 真机 smoke：make build → 带 token curl /debug/pprof/ 200；无 token 401；token 空时 404。
6. docs zh/en：README 观测章节补 pprof + /metrics/prometheus 说明；前端 README 组件行 += 运行详情。
7. spec §6 偏差回写。

## 5. 风险

- pprof 的 profile 端点（CPU 30s）在测试中会拖慢 → 测试只打 index/heap（debug=1 即时返回）。
- Prometheus 转换遍历 expvar 全局 Map —— 测试断言用"包含"而非精确行集，避免 -count 交叉污染。
- RunDetailDialog 三路并发请求失败任一路径 → 整体错误态（简单一致，不做部分降级）。

## 6. 执行偏差

（执行后回写）
