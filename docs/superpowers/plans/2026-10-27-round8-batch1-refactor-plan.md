# Round-8 批1 结构治理 实施计划

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 httpapi/server.go（727 行）按职责拆为 8 个文件、runtime/runner_eino.go 拆出 drain.go，纯移动零行为变化。

**Architecture:** 同包内文件拆分（不新建包、不改任何签名/逻辑/路由）；现有 30+ httpapi 测试与全量后端测试为回归安全网；每完成一组移动立即 build+test。

**Tech Stack:** Go 1.27 · gofmt/go vet · 现有测试套件

**Spec:** docs/superpowers/specs/2026-10-27-structure-frontend-mcp-design.md §批1

**注：本计划是"移动"型重构——被移动代码就是现有代码本身，计划以精确边界 + 流程描述代替代码罗列。执行时严禁顺手修改任何语句。**

---

## 现状函数地图（2026-10-27 实测，执行前如行号漂移以函数名为准）

server.go（package httpapi，imports L3-23 含 context/crypto/subtle/database/sql/encoding/json/errors/expvar/math/net/http/strconv/strings/time + 6 个 internal 包；SSE follow 变量 L26-30）：

| L 号 | 成员 | 去向 |
|---|---|---|
| 1-30 | package+imports+SSE follow 变量 | 保留 server.go |
| 32-46 | type Server struct | 保留 |
| 48-101 | ToolConfigProvider/ToolStatus/SkillsStatus/SkillInfo/MCPStatus/MCPServerInfo/ToolInfo/ToolConfigIn/MCPServerIn 类型 | 保留（settings 域类型，但被 Option 签名引用，留在 server.go 最稳） |
| 103-131 | Option/serverOptions/WithToolConfigProvider/WithEventHub/New | 保留 |
| 134-156 | routes() | routes.go |
| 158-172 | ownerOf/writeJSON/writeErr | helpers.go |
| 175-229 | auth/securityHeaders/rateLimit | middleware.go |
| 231-249 | handleHealth/handleReady/handleVersion | handlers_system.go |
| 251-323 | createSessionReq + sessions CRUD + handleListMemories | handlers_sessions.go |
| 326-576 | handleListRuns/createRunReq/runs CRUD/handleEvents(SSE)/cancel/resume/decideReq/approvals | handlers_runs.go |
| 578-683 | modelSettings + model/tools 设置 handlers + updateModelSettingsReq | handlers_settings.go |
| 685-712 | parseAfter/itoa64 | helpers.go |

runner_eino.go（package runtime）：drain(L320) / drainStream(L391) / emit(L418) / tokenFlushInterval(L25) → drain.go；其余（类型、gatewayTool、build、executeNew/executeResume、shortHash、cancelRegistry、toolObs）保留。

---

### Task 1: 基础设施三文件（routes / middleware / helpers）

**Files:**
- Create: `backend/internal/api/http/routes.go`、`middleware.go`、`helpers.go`
- Modify: `backend/internal/api/http/server.go`（删除对应段）

- [ ] 1.1 创建 routes.go：`package httpapi` + 从 server.go 原样剪切 routes() 整段（L134-156）+ 该函数所需 imports（net/http + 其引用的 handler 均同包无需 import；若有内部包引用以 build 报错为准补齐）
- [ ] 1.2 创建 middleware.go：剪切 auth/securityHeaders/rateLimit 三方法（L175-229）+ imports（context/crypto/subtle/net/http/strings/time/math + observability）
- [ ] 1.3 创建 helpers.go：剪切 ownerOf/writeJSON/writeErr/parseAfter/itoa64（L158-172 + L685-712）+ imports（encoding/json/net/http/strconv/strings 视实际）
- [ ] 1.4 server.go 删除上述段，补齐/修剪 imports
- [ ] 1.5 验证：`cd backend && go build ./... && go vet ./internal/api/http/ && go test ./internal/api/http/ -count=1 && gofmt -l internal/api/http/`（期望：测试全绿、gofmt 空输出）

### Task 2: 四个 handler 域文件

**Files:**
- Create: `handlers_system.go`、`handlers_sessions.go`、`handlers_runs.go`、`handlers_settings.go`
- Modify: `server.go`（删除对应段 + SSE follow 变量留在 server.go）

- [ ] 2.1 handlers_system.go：handleHealth/handleReady/handleVersion（L231-249）
- [ ] 2.2 handlers_sessions.go：createSessionReq + handleCreateSession/handleGetSession/handleDeleteSession/handleListSessions/handleListMemories（L251-323）
- [ ] 2.3 handlers_runs.go：handleListRuns/createRunReq/handleCreateRun/handleGetRun/handleEvents/handleCancel/handleResume/decideReq/handleApproval/handleListApprovals（L326-576）+ imports（context/encoding/json/errors/expvar/net/http/strconv/strings/time + domain/eventshub/store/service 视实际；SSE follow 变量被 handleEvents 引用但**留在 server.go**——同包可见，不搬）
- [ ] 2.4 handlers_settings.go：modelSettings/handleGetModelSettings/handleGetTools/handleUpdateTools/updateModelSettingsReq/handleUpdateModelSettings（L578-683）+ imports（context/encoding/json/net/http/strings + config/domain 视实际）
- [ ] 2.5 server.go 只剩：package/imports/SSE follow 变量/Server 类型组/Option/New/Handler（目标 ≤150 行）
- [ ] 2.6 验证同 1.5 + `wc -l internal/api/http/server.go`（≤150）

### Task 3: runtime drain.go

**Files:**
- Create: `backend/internal/runtime/drain.go`
- Modify: `backend/internal/runtime/runner_eino.go`

- [ ] 3.1 drain.go：剪切 tokenFlushInterval（L25 及其注释）/ drain / drainStream / emit（L320-433）；imports 以 build 报错为准（预计 context/time/schema/domain）
- [ ] 3.2 runner_eino.go 删除对应段并修剪 imports
- [ ] 3.3 验证：`cd backend && go build ./... && go vet ./internal/runtime/ && go test ./internal/runtime/ -count=1 && gofmt -l internal/runtime/`（runtime 测试含 emit 两阶段/ToolObs/memory_block 等既有用例）

### Task 4: 全量验收 + 提交

- [ ] 4.1 `cd backend && go build ./... && go vet ./... && go test ./... -count=1 -timeout 600s`（全绿；integration ~22s、evals 含）
- [ ] 4.2 `make test-eval`（15 场景 pass_rate 1）
- [ ] 4.3 路由零漂移：`grep -c 's.mux.HandleFunc' backend/internal/api/http/routes.go` = 21
- [ ] 4.4 前端零漂移：`cd frontend && npx tsc --noEmit && npx vitest run`（23 files/141 pass，本批无前端改动）
- [ ] 4.5 `git status --short` 干净（除历史 .omo/）后按任务分批提交：
  - Task 1 后：`refactor(http): split routes, middleware and helpers out of server.go`
  - Task 2 后：`refactor(http): split handlers into per-domain files`
  - Task 3 后：`refactor(runtime): extract drain/emit streaming pump into drain.go`
  - Task 4 收尾无需额外提交

## 风险与纪律

- **只剪切粘贴，不改一个字符的语句体**；若 build 报 unused import，仅删 import 行
- 行号会因前文删除而上移——始终以函数名定位（grep -n "^func "）
- 禁止顺手格式化未触碰区域（gofmt 全文件diff 除外，由 gofmt -l 验证兜底）
