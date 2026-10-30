# Round 25 设计：oauth 文档补遗 + evals 36 + 前端打磨

日期：2026-10-29 ｜ 状态：已批准（设计 'ok' m2801）

## 1. 背景

R24 为 MCP 服务器加了 OAuth client-credentials（PUT 接收 / GET 脱敏回显），但 docs/api/settings.md zh/en 未记录。评估体系 34 个场景未覆盖 FTS 中文路径与 oauth 校验拒绝。前端验证发现两个真空缺：全前端无任何复制功能（工具输出、run ID 均不可复制）；新会话无 run 时聊天区纯空白无引导。

## 2. 批 1：settings.md oauth 文档（zh/en）

PUT 的 `mcpServers` 行补充 oauth 字段：仅 `type: http` 支持，`{token_url, client_id, client_secret, scopes?}`，三字段必填（缺任一 400）。GET 响应的 servers 对象补 `oauth` 回显：`{token_url, client_id, scopes?, has_secret}`，**client_secret 永不回显**（与 headers 同规则）。各加一个带 oauth 的 PUT curl 示例。

验收：`grep -c oauth docs/api/settings.md docs/en/api/settings.md` ≥3 各文件。

## 3. 批 2：evals 34→36

文件 `backend/tests/evals/scenarios_round25_test.go`（package evals，`Run(t, Scenario{...})` 风格）：

1. `run_search_fts_cjk`（agent 类）：mock 模型完成 run（EnqueueText）→ PostCheck 用 `h.CreateRun` 造一条 goal 含 CJK 词组的 run → `GET /api/v1/runs?q=<词组>`（≥3 字节，FTS 短语路径）仅命中该 run；`?q=<短 ASCII>` 回退 LIKE 仍可用。
2. `mcp_oauth_stdio_rejected`（tool 类）：POST /settings/tools PUT 带 `mcpServers:[{name,command,oauth:{...}}]`（stdio+oauth）→ 400，body 含 'oauth is only supported for http servers'；去掉 oauth 重试同 payload → 200。

README zh/en 场景计数 34→36（工具级 22 + agent 级 14），scenarios_round25_test.go 进新增场景文件列表。

## 4. 批 3：前端打磨

### 4.1 复制按钮

- `ToolCallsCard`：每个工具调用的 output `<pre>` 右上加 Copy 图标按钮（`navigator.clipboard.writeText`；不支持时降级隐藏）；点击后 1.5s 显示 Check 图标反馈。
- `RunDetailDialog`：标题区 run ID 旁加同样的复制按钮。
- i18n：`common.copy` 复制/Copy、`common.copied` 已复制/Copied。
- 组件内小 hook `useCopy()`（copied 状态 + setTimeout 复位）；不建全局抽象。

### 4.2 聊天空状态

- `ChatMessageList`：`messages.length === 0` 时渲染居中空状态卡片（Bot 图标 + `chat.empty` zh「还没有运行，创建第一个试试」/ en "No runs yet — create your first one" + 按钮触发现有「新建运行」入口）。
- 触发方式：复用现有打开 NewRunDialog 的 store/props 链路；若无现成回调 prop，则通过 ui-store 加 `newRunOpen` 布尔（最小闭环，Header/NewRunDialog 现有入口同步改造）。

## 5. YAGNI

不做：键盘快捷键、拖拽、run ID 全局常显、复制整个事件流、空状态插画。

## 6. 验收

1. settings.md zh/en oauth 命中；2. make test-eval 36 pass_rate 1；3. 后端 go test 19 pkgs ok；4. 前端 tsc 0 / lint 0 / vitest 全绿（copy + empty-state 新测试）；5. spec 偏差回写。

## 7. 执行偏差

- **ErrInvalidToolConfig 语义修复（产品级发现）**：设计假设 stdio+oauth PUT → 400，实测真路径返回 500 internal（handler 把一切 SaveTools 错误统一映射 500）。修复：httpapi 新增 `ErrInvalidToolConfig` 哨兵，app 层 mcpServersFromIn 校验失败时 %w 包装，handlePutTools errors.Is → 400 bad_request（含校验消息）。settings.md 中「缺任一 400」由此为真。
- **Stub provider 忠实化**：tests/testutil stubToolProvider.SaveTools 现运行 mcp.ValidateSpecs（首版转换漏拷 OAuth 字段导致场景仍 200——补拷后 RED→GREEN）。
- **evals oauth 场景**：需 `NewHarness: NewToolConfigHarness`（普通 harness 无 tools provider，PUT 直接 501）。
- **聊天空状态已存在**：旧实现硬编码英文 "No tasks yet" 且无按钮——本批为「替换旧块」（i18n + CTA 按钮），并经 ui-store `newRunOpen` 让 ChatInputBar 的 NewRunDialog 成为双入口；设计稿"新增空状态"表述修正为"改造既有空状态"。
- **RunDetailDialog 标题原本不显示 run ID**——本批新增 runId 行 + CopyButton（复制目标由此而来）。
- **listApprovals mock** 返回 `Approval[]`（非包装对象）；http_fetch 等 3 个既有 ToolCallsCard 断言不变。
- Task 1 文档零偏差（顺带修复 mcp.servers 行缺 R12 type/url 字段）。
