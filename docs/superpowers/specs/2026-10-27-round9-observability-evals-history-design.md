# 第 9 轮完善优化设计：HTTP 可观测性 + Evals 扩展 + 跨会话 Run 历史

日期：2026-10-27 ｜ 状态：已批准（设计 §1/§2/§3 ok，m1232）

## 1. 背景与缺口

八轮优化后仍存在三个已验证缺口：

1. **HTTP 层零可观测性**：`internal/api/http` 无任何请求日志、无请求 ID（grep `slog.Info|X-Request|requestID` 为空）；仅 main.go 有生命周期日志。SSE/流式问题无法排查。
2. **Evals 覆盖不全**：15 个场景未覆盖 settings API、子 agent 预算钳制、http_fetch 白名单命中、knowledge 空结果、file_list。
3. **前端无跨会话视角**：`GET /api/v1/runs` 强制 `session_id`（缺省 400）；用户只能逐会话翻历史。

已确认无需处理：优雅停机已有（signal.NotifyContext + srv.Shutdown）；文件规模已收敛（最大 task_repo.go 433L）。

## 2. 结构

一 spec 三批，顺序 批1 → 批2 → 批3，各自独立计划文件 + TDD 执行 + 全量验收：

| 批次 | 内容 | 主要文件 |
|---|---|---|
| 批1 | requestID + 请求日志中间件 | `internal/api/http/observe.go`(新)、middleware.go、server.go、app.go |
| 批2 | 5 个新 eval 场景（15→20） | `tests/evals/scenarios_settings_test.go`(新)、scenarios_agent_test.go |
| 批3 | 跨会话 run 历史 | store/task_repo.go、handlers_runs.go、frontend RunsHistoryDialog(新)、Header.tsx |

## 3. 批次设计

### 3.1 批1 HTTP 可观测性

- 新文件 `observe.go`：
  - `requestID`：请求头 `X-Request-ID` 非空则沿用（截断 64 字符），否则生成 16 字节 hex（crypto/rand）；写入响应头 + `context.Context`（`ctxKey` 私有类型，`RequestIDFrom(r)` 导出读取）。
  - `requestLog`：包装 ResponseWriter 捕获 status（默认 200），结束后 `log.Info("http request", "method",…, "path",…, "status",…, "duration_ms",…, "request_id",…)`。
- 链序：`securityHeaders(requestID(requestLog(auth(rateLimit(mux)))))` —— 日志捕获最终状态（含 401/429），安全头仍最外层；auth 在 rateLimit 外保持 401 先于 429。
- `Server` 增 `log *slog.Logger` 字段，`New()` 中 nil → `slog.Default()`（与 runtime.Deps.Log 同模式）；app.go 传 `a.log`。
- 测试 `server_observe_test.go`：无头生成 ID（长度 32 hex）、带头沿用（原值返回）、log 行含五字段（`slog.NewTextHandler(&bytes.Buffer{})` 捕获）、401/429 响应也带 `X-Request-ID`。

### 3.2 批2 Evals 扩展（15 → 20）

| # | 名称 | 关键断言 |
|---|---|---|
| 16 | settings_toggle_persists | `NewToolConfigHarness`；PUT `/settings/tools` `{"skillsEnabled":false}` → GET 返回 disabled；`SavedToolConfigs()` 记录该载荷 |
| 17 | subagent_child_budget_clamped | 父 Budget.MaxToolCalls=10 → tasks 单子代理 → `ListChildrenByRun` 读子 run：`Budget.MaxToolCalls == 4`（40% 收紧，cap 30 不触发） |
| 18 | http_fetch_allowlist_hit | httptest.Server + SetHTTPAllowlist(其 host)；EnqueueToolCall http_fetch → MidRun 审批（external/always 先审批）→ tool.completed 存在 + run 成功 |
| 19 | knowledge_search_miss | 空 knowledge 目录（withTempKnowledge 不写文件）→ run 成功、tool.completed 存在 |
| 20 | file_list_directory | writeFileRel 两文件 → file_list 输出含两文件名（args 无 pattern = 单层 ReadDir） |

全部复用现有框架（Expect/MidRun/PostCheck/NewHarness），**不改框架**。已核实：`clampChildBudget` 40% + cap（model 20/tool 30/iter 10/dur 180/out 8KB）；`http_fetch` Approval=always 审批先于 allowlist 检查。

### 3.3 批3 跨会话 Run 历史

- 后端（向后兼容，无新端点）：
  - `TaskRepo.ListRunsByOwner(ctx, ownerID string, limit int) ([]domain.Run, error)`：`WHERE owner_id = ? AND parent_run_id IS NULL ORDER BY created_at DESC LIMIT ?`（只取顶层 run，排除子代理 run）；列扫描复制 ListRunsBySession 模式；limit 语义同（≤0 或 >100 → 50）。
  - `handleListRuns`：`session_id` 变可选 —— 有值走原路径（含 GetSession 校验），为空调 `ListRunsByOwner`。
- 前端：
  - `api.ts`：`listRuns(sessionId?, limit=50)` —— sessionId 可选，空时不带 query。
  - 新 `RunsHistoryDialog.tsx`：打开时拉全量 run；表格列 goal / session_id 短 ID / 状态徽标（复用 runStatusClasses）/ error_code / 耗时；点击行 → `useSessionStore.setActiveSessionId(run.session_id)` + 关闭弹窗；空态「（暂无运行记录）」；错误 + 重试。
  - `Header.tsx`：新增「运行历史」按钮（History 图标）。
- 测试：store 层 ListRunsByOwner（多会话混合 + 子 run 排除 + limit）；http 层不带 session_id 200 / 带值走原路径；前端 dialog（渲染/跳转/空态/错误重试，vi.mock api）。

## 4. 验收

1. `cd backend && go build ./... && go vet ./...` 干净
2. `go test ./... -count=1 -timeout 600s` 全绿；`make test-eval` 20 场景 pass_rate 1
3. 前端 `npx tsc --noEmit` 0 错误；`npx vitest run` 全过
4. 请求日志与 X-Request-ID 在 401/429/404 响应上同样存在（server_observe_test 断言）
5. 文档同步：README zh/en API 表 runs 行注明 session_id 可选；docs/api/runs.md zh/en 更新；backend/README zh/en 提及请求日志

## 5. 风险与非目标

- 风险：批3 repo 新方法复制 19 列扫描（漂移风险 → 两方法共存期若加列需同步改两处，验收后考虑合并提取；本轮不做）；批2 #18 依赖审批时序（MidRun 模式已验证于 file_edit/http_fetch 场景）。
- 非目标（YAGNI）：trace/span/OTel、访问日志开关配置、run 历史分页 UI、会话内搜索、per-路由日志级别。
