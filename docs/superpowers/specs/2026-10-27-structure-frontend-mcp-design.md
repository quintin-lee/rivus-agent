# 第 8 轮完善优化：结构治理 + 前端流式打磨 + MCP 深化 设计

日期：2026-10-27 · 状态：已批准（用户 ok，m1094）

## 1. 背景与问题

七轮迭代后扫描发现三块短板：

1. **结构热点**：`backend/internal/api/http/server.go` 膨胀至 727 行（20 路由 + 3 中间件 + 全部 handler + SSE 长函数挤在一个文件）；`backend/internal/runtime/runner_eino.go` 465 行（EinoRunner 结构/构建/执行 + drain/emit 事件泵混在一起）。后续任何 handler 改动都在巨型文件上进行，冲突面与回归面大。
2. **前端体验缺口**：AssistantBubble 纯文本渲染（无 markdown，grep 证实）；流式期间无打字指示；failed run 无重试入口。预算进度已有（SystemStatusBubble），不在范围内。
3. **MCP 健壮性**：`Manager.connect` 失败后状态永远停在 error，直到用户改设置触发 Reconcile 才重试；`Manager.Call` 无超时，死传输可能挂住 agent 迭代（120s 才被外层墙钟兜住）。

## 2. 方案总览（3 批，顺序执行）

| 批 | 主题 | 新依赖 | 行为变化 |
|---|---|---|---|
| 批1 | 结构治理（纯移动） | 无 | 零 |
| 批2 | 前端流式打磨 | react-markdown（唯一新增） | 3 项增强 |
| 批3 | MCP 深化 | 无 | 重连 + 超时 |

### 批1：server.go 拆分（727 → ~130 + 6 文件）

仅移动代码，不改任何逻辑/路由/签名。目标布局（`backend/internal/api/http/`）：

| 文件 | 内容（原 server.go 行号） |
|---|---|
| `server.go`（保留） | Server 类型/接口/Option/New/Handler（L32-133） |
| `routes.go` | routes()（L134-156） |
| `helpers.go` | ownerOf / writeJSON / writeErr / parseAfter / itoa64（L158-172, L685-712） |
| `middleware.go` | auth / securityHeaders / rateLimit（L175-229） |
| `handlers_system.go` | handleHealth / handleReady / handleVersion（L231-249） |
| `handlers_sessions.go` | 会话 CRUD + memories（L251-323） |
| `handlers_runs.go` | runs / events(SSE) / cancel / resume / approvals（L326-576） |
| `handlers_settings.go` | modelSettings + model/tools 设置（L578-683） |

`runtime/runner_eino.go`（465 行）拆出 `runtime/drain.go`：drain / drainStream / emit / tokenFlushInterval（流式泵专职文件）。runner_eino.go 留 EinoRunner 构建与执行。

验证：build/vet/gofmt 干净；全量测试绿（httpapi 现有 30+ 测试为安全网）；README 路由计数 21/21 不变；`grep -c "HandleFunc"` 总数不变。

### 批2：前端流式打磨（3 项，TDD）

1. **Markdown 渲染**：AssistantBubble 接入 `react-markdown`（默认不渲染裸 HTML —— 无 rehype-raw，天然防 XSS；代码块保持 `<pre>` 等宽）。唯一新增前端依赖，用户已批准（m1094）。
2. **打字指示器**：run running 且 SSE streaming 时，最新 assistant 消息尾部渲染动画三点（纯 CSS animation，复用现有流式状态 `useUiStore.sseStatus` + run status）。
3. **失败重试**：failed/cancelled run 气泡下渲染「重试」按钮，点击复用该 run 的原始 goal/budget 重新 `createRun`（从 run 对象回填，不依赖额外 API）。

测试：每个组件/派生逻辑对应 vitest 用例；`npx tsc --noEmit` 0 错误。

> **执行偏差（批2 实际落地，commit 2d3fbbf / 73c1cea）**：
> 1. Markdown 渲染**已存在**——`result-block.tsx` 早已用 `ReactMarkdown + remarkGfm + rehypeSanitize`（XSS 安全）渲染 `result_json`，`react-markdown` 也已在 package.json 中；此项 0 工作量，未做改动（spec 初稿「唯一新增依赖」前提有误）。
> 2. 打字指示器落在 `SystemStatusBubble`（run 状态卡）而非「最新 assistant 消息尾部」——assistant 消息只在终态才存在，流式期间没有可依附的消息节点；触发条件改为 `run running && partialText 非空`（token 流式增量），比 SSE 状态更准确。
> 3. 「失败重试」实为**修复一个已存在的坏按钮**：原实现对流式失败前的 `resumeRun` 会被后端拒绝；现改为「重新运行」——用原 run 的 goal/budget/constraints/success_criteria 经 `submitRun` 创建新 run（`onRerunRun` 回调由 ChatMessageList 提供）。

### 批3：MCP 深化（2 项，TDD）

1. **自动重连**：`Manager` 新增 `specs []ServerSpec`（SpawnAll/Reconcile 成功后记住）+ `StartReconciler(ctx, interval)`——后台 ticker 周期调用 `Reconcile(ctx, m.snapshotSpecs())`，error 状态的 server 自动重试，up 的跳过。周期固定 **30s**（用户批准，不加配置项）。`app.go` 启动时开 goroutine；测试用注入短 interval + FakeTransportFactory。
2. **Call 超时**：`Manager.Call` 内 `ctx, cancel := context.WithTimeout(ctx, m.callTimeout)`（常量 30s），超时返回 `mcp call timeout` 错误；测试用阻塞 Transport（新增 testOnly blocking transport）断言 ≤1s 返回错误。

> **执行偏差（批3 实施时确认）**：
> 1. Call 超时实现为 goroutine + select 竞速——`Client.rpc` 的 `ReadLine` 无 ctx 感知（阻塞读），超时只保证**调用方**及时返回；被卡住的底层调用会继续持有该 client 的互斥锁直到 transport 解除阻塞。属现有架构约束，plan 不改 Transport 接口。
> 2. 30s 以 `Manager.callTimeout` 字段默认值落地（`NewManager` 设 30s），测试注入 50ms 短超时；阻塞 transport 组合 FakeTransport（initialize/tools/list 正常放行，第 3 个请求 tools/call 阻塞 WriteLine），避免 SpawnAll 自身挂死。

文档：backend/README zh/en MCP 小节补一句重连与超时语义。

## 3. 不做（YAGNI）

- server.go 不改任何 handler 逻辑/错误码/路由路径（纯移动）
- 不引入 markdown 语法高亮（rehype-highlight）、不渲染裸 HTML
- 不做 run 历史跨会话页、不做预算编辑（创建时已有）
- MCP 不做 SSE/HTTP transport、不做进程崩溃检测钩子（重连已覆盖恢复）、重连不加配置
- 不动 runner_child.go / runtime.go

## 4. 风险

| 风险 | 缓解 |
|---|---|
| 拆分时遗漏 import/引用 | 每个文件拆完即 `go build`；全量测试兜底 |
| react-markdown 与现有样式冲突 | 限定在 AssistantBubble 内部容器，Tailwind prose 类作用域化 |
| Reconciler goroutine 泄漏 | ctx 取消即退出；app 生命周期绑定 |
| Call 超时误伤慢工具 | 30s 远大于现有工具级 30s timeout 单工具上限场景；错误信息可辨识 |

## 5. 验收标准

1. 批1 后 `go build ./... && go vet ./...` 干净，`gofmt -l` 空；`go test ./... -count=1` 全绿；路由总数 21/21；server.go ≤150 行
2. 批2 后 `npx tsc --noEmit` 0 错误；`npx vitest run` 全过（新增 ≥6 用例）；markdown/打字/重试三项在测试中均有断言
3. 批3 后 mcp 包测试覆盖：error server 自动重连成功、Call 超时路径；`go test ./internal/mcp/ -count=1` 绿
4. 全量：backend build/vet/test + evals（15 场景）+ 前端 tsc/vitest 全绿；文档（README zh/en MCP 段）同步
5. 每批独立 commit 序列，工作树最终干净
