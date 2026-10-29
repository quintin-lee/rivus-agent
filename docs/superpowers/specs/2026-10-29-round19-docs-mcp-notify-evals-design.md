# Round 19 设计：文档补遗 + MCP 通知推送 + evals 32

日期：2026-10-29 ｜ 状态：已批准（设计 'ok' m2257）

## 1. 背景

R17/R18 引入会话重命名与会话搜索后留下三处文档漂移：docs/api/sessions.md（zh/en）未记录 `q` 参数；CHANGELOG.md [Unreleased] 停在 R16。MCP 侧 `client.rpc` 为单请求-单响应，服务器插入通知行（无 id 的 JSON-RPC notification）会撞 id 校验报错，通知通道不可用；工具列表变化只能靠 30s Reconciler 轮询感知。evals 30 场景未覆盖 R17/R18 的会话 API。

## 2. 批 1：文档补遗 + CHANGELOG 同步（纯文档）

- `docs/api/sessions.md` 与 `docs/en/api/sessions.md` 的 `## GET /api/v1/sessions` 小节补 `q` query 参数：标题大小写不敏感子串匹配（`LOWER(title) LIKE`），省略 = 全量；附 curl 示例。
- `CHANGELOG.md` [Unreleased] Added 追加：会话重命名（`PATCH /api/v1/sessions/{id}` + 前端行内改名）、会话搜索（`GET /api/v1/sessions?q=` + 前端 200ms 防抖搜索框）。
- 验收：两个 sessions.md 均含 `q` 参数说明；CHANGELOG 两条新条目存在。

## 3. 批 2：MCP 服务器推送（notifications/tools/list_changed）

### 3.1 client.go 通知跳过

`rpc` 读循环改为：每读一行先判型——有 `method` 且无 `id`（JSON-RPC notification，id 缺省解析为 0，而本客户端请求 id 从 1 递增）→ 记为待分发通知并继续读下一行；否则视为响应，按现有 id/错误校验处理。通知在**释放 c.mu 之后**统一分发到回调，避免回调重入死锁。新增 `SetNotificationHandler(func(method string, params json.RawMessage))`（nil 安全）。

### 3.2 manager.go 通知接线

`connect` 成功置 up 后注册 handler：`notifications/tools/list_changed` → 起 goroutine 调 `relistTools(spec, client)`——重新 `tools/list`（受既有 c.mu 串行化约束，排队等当前 rpc 完成），成功则更新 `m.status[name].Tools`；失败仅记 status 错误信息，不断开连接。

### 3.3 测试

- client：scripted transport（ReadLine 依次返回通知行、正常响应）→ rpc 返回正确 result 且 handler 收到 method/params；无 handler 时不 panic。
- manager：connect 后注入通知（transport 在下一次 rpc 前先吐一行 `notifications/tools/list_changed`）→ status.Tools 刷新为扩容后的工具集。

### 3.4 YAGNI

不做独立 SSE 长连接传输（GET /sse）；不处理 tools/list_changed 之外的通知类型；不做通知节流。

## 4. 批 3：evals 30→32

Scenario 框架 run-based；会话 API 场景以最小 text-only run 进入 PostCheck 验证 API 行为：

- `session_rename_persists`：PostCheck 内 PATCH `/api/v1/sessions/{id}`（body `{"title":"renamed-eval"}`）→ 200；GET 断言新标题；PATCH 空标题 → 400。
- `session_search_filters`：PostCheck 内再建一个不同标题会话 → `GET /api/v1/sessions?q=<关键词>` 命中其一、不含另一；`?q=` 省略返回全部。
- 30→32（21 tool + 11 agent），backend README zh/en 计数回写。

## 5. 风险

- 通知分发在 goroutine 中触发 relistTools，与进行中 rpc 共享 c.mu——排队等待，不并发读流（架构约束与 R8 相同）。
- 通知风暴（服务器高频推送）可能产生 goroutine 堆积：relistTools 前检查 status 是否已 up 且工具未变则跳过写入（读侧比对，不加锁升级）。

## 6. 验收清单

1. sessions.md zh/en 含 q 参数说明；CHANGELOG 两条新条目。
2. MCP client/manager 通知测试绿；`go build/vet` 干净。
3. evals 32 场景 pass_rate 1；README zh/en 计数 32。
4. 后端 go test 全绿；前端 tsc/lint/vitest 全绿（前端零改动预期）。
5. spec 本节回写执行偏差（如有）。

## 7. 执行偏差

- `Transport` 接口的 `Stop()` 返回 `error`——计划中测试 transport 的裸 `Stop()` 编译不过，改为 `Stop() error { return nil }`。
- pushTransport 按自查笔记 4 实现为独立完整假服务器（pending 请求槽 + initialize/tools/list/tools/call canned 响应 + inject 通知队列 + tools 队列），未嵌入 FakeTransport。
- evals 场景采用 `Run(t, Scenario{...})` 包级函数调用（与 R16 场景文件一致），而非计划草稿中的 `.Run(t)` 方法风格。
- README evals 计数行实际位于 backend/README.md:144 与 backend/README.en.md:148（根 README 无 evals 小节）。
- 批 3 场景为 PostCheck 断言型，首跑即绿（预期成立）；批 1/批 2 均按计划 RED→GREEN，无其他偏差。
