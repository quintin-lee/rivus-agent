# Round 20 设计：分页 / pprof 开关 / MCP SSE 长连接

日期：2026-10-29 ｜ 状态：已批准（设计 'ok' m2325，全套 1+2+3）

## 1. 批 1：会话/run 分页（offset 式）

现状：`ListSessions` 无 LIMIT（全量返回）；runs 两方法仅 limit 钳制无分页。单用户工具选 offset 式（YAGNI 游标稳定性）。

- store：`ListSessions(ctx, ownerID, q, limit, offset)`（limit 默认 ≤0→50，>200→200，新增钳制）、`ListRunsBySession` / `ListRunsByOwner` 加 `offset int`；SQL `LIMIT ? OFFSET ?`
- handlers：两个端点读 `offset` query（`strconv.Atoi`，负数/非法→0）
- 前端：`api.listSessions(q, offset=0)`、`listRuns(sessionId, limit, status, offset=0)`；SessionList 与 RunsHistoryDialog 返回数等于 limit（满页）时渲染「加载更多」，点击 offset 累加追加；i18n `common.loadMore` zh/en
- 测试：store 单测（翻页/越界空）、集成（offset 过滤）、前端（满页按钮 + 点击带 offset）

## 2. 批 2：pprof 独立开关 + exhaustive-deps 升 error

- config：`Pprof *bool`（yaml `pprof`，env `AGENT_PPROF`，"true"/"false" 解析，非法忽略）；nil = 现行为（auth 开启即开）；显式覆盖。server.go 三分支赋值
- 文档：.env.example + README zh/en 配置表 + system.md pprof 段
- eslint.config.js：`exhaustive-deps` 'warn'→'error'；现有违规先修
- 测试：config 单测（nil/true/false/非法）；httpapi 测试（AGENT_PPROF=false + token → pprof 404；AGENT_PPROF=true 无 token → pprof 注册但 401——与 auth 行为一致）

## 3. 批 3：MCP SSE 长连接（http 型服务端主动推送）

R19 通知靠请求响应流搭车；本批给 http 型 transport 加独立 GET SSE 长连接，合并进既有行队列——Client/Manager（R19 交错行解析）零改动。

- http.go：`httpTransport` 增加后台 goroutine——`GET url`（Accept: text/event-stream）流式读，SSE `data:` 行解码为原始 JSON 行，推入有缓冲行队列（cap 64）；`ReadLine` 改为从队列弹出（WriteLine 把 POST 响应体推入队列）；GET 断开→静默重连（5s，`stopped` 原子标记终止）；`Stop` 关闭两流 + 队列防双关
- 测试：httptest POST JSON-RPC + GET 流推 2 条通知 → handler 收到 + rpc 正常 + Stop 清理（无 goroutine 泄漏断言从简：Stop 后 ReadLine 立即报错）

## 4. YAGNI

游标分页、总数计数、MCP SSE OAuth、GET 流心跳、指数退避。

## 5. 验收

1. 分页：store/集成/前端测试绿；`?offset=` 生效
2. `AGENT_PPROF=false`+token → 404；`AGENT_PPROF=true` 无 token → 401；nil 行为不变
3. exhaustive-deps=error 且 lint 0 违规
4. SSE 推送测试绿 + mcp 全包绿
5. 全量：go 19 pkgs、evals 32、tsc 0、lint 0、vitest 全绿
6. 文档同步 + spec 偏差回写

## 6. 执行偏差

（执行后回写）
