# Round 31 设计：MCP OAuth UI + evals 45 + 运行时间线深化

日期：2026-11-02 ｜ 状态：已批准（'ok'）

## 1. 背景

R24/R28 落地了后端 MCP OAuth（client-credentials + 授权码，`MCPOAuthIn`/`MCPOAuthInfo` DTO 完整，`client_secret` 永不回显用 `has_secret`），但**前端 ToolsPanel 完全没有 OAuth UI**（只支持 stdio 的 name+command）；`MCPServerIn` 前端类型缺 `type/url/oauth` 字段。evals 停在 42 场景。运行详情时间线只覆盖 14 类事件标签、截断最后 50 条、无工具名摘要。

## 2. 方案（3 批）

### 批1 前端：MCP OAuth 配置 UI（ToolsPanel）

- `types.ts`：`MCPServerIn` += `type?: string; url?: string; oauth?: MCPOAuthIn`；`MCPOAuthIn{token_url, client_id, client_secret?, scopes?}`；`MCPServerInfo` += `type?/url?/oauth?`（oauth 为回显 `MCPOAuthInfo` 含 `has_secret`，不回填 secret）。
- `ToolsPanel.tsx`：每个 server 卡片可选「HTTP 模式」开关（`type:'http'` + url 输入）；「OAuth」开关勾选后展开 token_url / client_id / client_secret（密码框，占位「留空=保留已存」）/ scopes（逗号分隔）；授权码模式（authorize_url + redirect_port）可选行。保存时组装 `oauth`：token_url 与 client_id 均空 → 不带 oauth；client_secret 留空 → 省略该字段（后端 `has_secret` 语义保留旧 secret）。
- i18n：`mcp.*` 键（工具设置标题/启用/服务器名/command/移除/添加/http模式/URL/oauth/token_url/client_id/client_secret/secretPlaceholder/scopes/授权码/authorize_url/redirect_port）zh+en；ToolsPanel 现存硬编码中文一并迁移（最小范围）。
- 测试（TDD）：`ToolsPanel.test.tsx` ① 默认渲染 server 表单；② 勾选 http+oauth 保存 → `api.saveTools` 收到 `{type:'http',url,oauth:{token_url,client_id,scopes}}`（secret 空省略）；③ oauth 关闭 → 无 `oauth` 字段；④ secret 填值 → 载荷含 client_secret。

### 批2 evals：42 → 45 场景

新增 `scenarios_round31_test.go`（package evals）：
1. `mcp_oauth_authcode_validation`（tool，NewHarness=NewToolConfigHarness）：PUT /settings/tools 带 `mcpServers:[{name, type:'http', url, oauth:{token_url,client_id,client_secret,authorize_url}}]`（缺 redirect_port）→ 400；补 `redirect_port` → 200。PostCheck 经 h.Do。
2. `s3_tools_absent_by_default`（agent）：默认空 S3 配置下 EnqueueToolCall 指向 `s3_read` → 工具不存在，run 经 fallback succeeded，且事件流中**无** tool.completed `s3_read`（断言 RequiredEvents 不含 + 终态 succeeded）。防 R30 条件注册回归。
3. `approval_decided_flow`（agent）：file_edit 审批流 EnqueueToolCall → waiting_approval → MidRun POST approvals approve:true → succeeded + 事件含 `approval.decided`。

### 批3 前端：运行详情时间线深化

- i18n：补齐 `domain/event.go` 全部 28 类事件的 `timeline.*` 标签（zh+en；缺失的如 plan/step/model.requested/tool.requested/started/authorized/denied/approval.decided/checkpoint.saved/run.created/resumed 等）。
- `RunDetailDialog.tsx` 时间线条目：`tool.*` 显示工具名（`parseToolCalls` 同源的 `payload.tool`/`text`），`step.started/finished` 显示 step description（取 run.steps 匹配 `step_index` 不可得则省略）；新增「展开全部」切换（`expanded` state，默认 `slice(-50)`，展开为全量）+ i18n `timeline.expandAll/collapse`。
- 测试（TDD）：① 每类事件类型在 zh 字典有对应 `timeline.*` 键（遍历 `domain/event.go` 常量表——以 TS 侧维护的已知类型数组为准）；② 有 tool.completed 事件时时间线行显示工具名；③ 「展开全部」点击后行数 = events.length（>50 时）。

## 3. YAGNI

- 不做：OAuth 授权码浏览器自动跳转（授权码流程保持后端 stderr 提示链接，UI 只配置字段）；secret 的「已存」视觉标记（依赖 `has_secret`，仅在信息区显示，不回填到输入框）；时间线虚拟滚动；evals 新增 S3 真实连接场景。

## 4. 验收

1. `go build ./... && go vet ./...` 通过；`go test ./...` 全绿；`make test-eval` 45 pass_rate 1。
2. 前端 `npx tsc --noEmit` 0 错、`npm run lint` 0、`npx vitest run` 全绿（新增测试计入）。
3. 文档同步：README evals 计数 42→45（zh/en）；ToolsPanel OAuth 说明。

## 5. 风险

- ToolsPanel 现无测试文件，需新建；saveTools 载荷组装逻辑建议抽为纯函数 `buildServerIn(srv)` 便于单测。
- 时间线「每类事件有标签」测试依赖 TS 侧类型数组与后端 event.go 同步——以后端 28 类为准手工枚举，漂移时补。
