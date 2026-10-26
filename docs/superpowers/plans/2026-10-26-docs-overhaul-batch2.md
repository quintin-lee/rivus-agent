# Docs Overhaul 批2 · API 参考 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:executing-plans to implement this plan (本仓库约束：不使用 subagent)。Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 19 条 REST 路由 + SSE 事件流产出逐端点手写 API 参考（`docs/api/` 7 篇中文 + `docs/en/api/` 7 篇英文镜像），事件目录覆盖 `domain/event.go` 全部 28 个常量。

**Architecture:** 纯文档批次，零代码变更。所有字段表、状态码、payload 形状、错误码均在计划内从代码提取固化（见「已核实事实」），写作时照抄，不凭记忆。英文版在中文定稿后于执行期派生（沿用批1 有意偏离：翻译以执行时中文定稿为唯一来源）。

**Tech Stack:** Markdown（手写，中英双语）、bash 校验脚本。

**Spec:** `docs/superpowers/specs/2026-10-26-docs-overhaul-design.md`（§2 批2、§3 批2、§6 验收 1/2/3/4/5/6）

---

## 已核实事实（写作时直接引用，禁止臆造）

### 通用

- **认证**：`AGENT_AUTH_TOKEN` 非空时，除 `/healthz`、`/readyz` 外全部路由要求 `Authorization: Bearer <token>`，否则 `401 {"code":"unauthorized","message":"missing or invalid token","retryable":false}`（`server.go` `auth`）
- **多租户归属**：请求头 `X-Owner-ID`，缺省 `"default"`；跨 owner 访问一律 404（不泄露存在性）
- **错误响应统一格式**：`{"code": "<string>", "message": "<string>", "retryable": <bool>}`（`writeErr`）
- **请求体上限**：1 MiB（`http.MaxBytesReader`），超限/坏 JSON → `400 bad_request`
- **Content-Type**：请求/响应均 `application/json`（SSE 除外，`text/event-stream`）
- **Idempotency-Key**：`POST /api/v1/runs` 请求头；同 key 重复提交返回同一 Run 且响应 `duplicated: true`（首次为 `false`）
- **预算只紧不松**：服务端上限缺省 `max_duration_seconds=600, max_model_calls=30, max_tool_calls=50, max_iterations=12, max_output_bytes=200000`（`config.go` 默认值）；客户端申请值 `<=0` 或 `> 上限` 时取上限（`Budget.ClampByServer`）
- **时间戳**：均为 Unix 毫秒整数（`created_at`/`updated_at`/`expires_at` 等）

### 领域结构（json tag 原样引用）

- `Run`（`domain/task.go:104-125`）：`id, session_id, parent_run_id?, owner_id, status, mode, goal, constraints?, success_criteria?, budget{max_duration_seconds,max_model_calls,max_tool_calls,max_iterations,max_output_bytes}, checkpoint_id?, attempt, idempotency_key?, error_code?, error_summary?, result_json?, created_at, updated_at, started_at?, finished_at?`（`?` = omitempty）
- `Step`（:128-138）：`id, run_id, step_index, description, status, dependencies?, result_summary?, started_at?, finished_at?`
- `Approval`（:151-164）：`id, run_id, tool_call_id, tool_name, args_hash, requested_by, approved_by?, status, reason?, expires_at, created_at, decided_at?`
- `SessionInfo`（:96-101）：`session_id, title, created_at, updated_at`
- Run 状态：`queued → running → {waiting_approval|paused|succeeded|failed|cancelled|timed_out}`（终态：`succeeded/failed/cancelled/timed_out`）
- Approval 状态：`pending/approved/rejected/expired`
- Step 状态：`pending/running/succeeded/failed/skipped/awaiting_approval`

### 逐端点（server.go handler 提取）

| # | 方法+路径 | 成功响应 | 错误 |
|---|---|---|---|
| 1 | POST `/api/v1/sessions` body `{title}` | 201 `{"session_id":"ses_..."}` | 400 / 500 internal |
| 2 | GET `/api/v1/sessions` | 200 `{"sessions":[SessionInfo]}` | 500 |
| 3 | GET `/api/v1/sessions/{id}` | 200 `{"session_id","title","created_at","updated_at"}` | 404 |
| 4 | DELETE `/api/v1/sessions/{id}` | 200 `{"status":"deleted"}` | 409 conflict（有活跃 Run）`"session has active runs; cancel them first"` / 404 |
| 5 | GET `/api/v1/runs?session_id=&limit=`（limit 缺省 50） | 200 `{"runs":[Run]}` | 400 `"session_id query param required"` / 404 / 500 |
| 6 | POST `/api/v1/runs` body `{session_id,goal,constraints?,success_criteria?,mode?,budget?}` + 头 `Idempotency-Key` | 201 `{"run_id","status":"queued","duplicated":false}` | 400 `"invalid goal/session"` / 500 |
| 7 | GET `/api/v1/runs/{id}` | 200 `{"run":Run,"steps":[Step]}` | 404 / 500 |
| 8 | GET `/api/v1/runs/{id}/events` | 200 SSE 流 | 404 |
| 9 | POST `/api/v1/runs/{id}/cancel` | 200 `{"status":"cancelled"}` | 409 `"run is not in a cancellable state"` |
| 10 | POST `/api/v1/runs/{id}/resume` | 200 `{"status":"running"}` | 409 `"run is not in a resumable state"` |
| 11 | POST `/api/v1/runs/{id}/approvals/{approval_id}` body `{approve:bool,reason?}` | 200 `{"status":"decided"}` | 409 `"approval already decided or expired"`（含 403 forbidden 映射为 409 `"forbidden"`） |
| 12 | GET `/api/v1/runs/{id}/approvals` | 200 `{"approvals":[Approval]}` | 404 / 500 |
| 13 | GET `/api/v1/settings/tools` | 200 `{skills:{enabled,available:[{name,description}]},mcp:{enabled,servers:[{name,status:"up"\|"down"\|"error",command,args?,tools:[{name,description}]}]}}` | 501 `tools_not_configured` / 500 |
| 14 | PUT `/api/v1/settings/tools` body `{skillsEnabled:bool,mcpEnabled:bool,mcpServers:[{name,command,args?}]}` | 200 `{}` | 501 / 400 / 500 |
| 15 | GET `/api/v1/settings/model` | 200 `{"provider","base_url","model","api_key_set":bool}` | — |
| 16 | PUT `/api/v1/settings/model` body `{provider?,base_url?,model?,api_key?}`（空串=不改） | 200 同 GET | 400 `"unsupported provider"`（仅接受 `openai_compat`）/ 500 |
| 17 | GET `/healthz`（免认证） | 200 `{"status":"ok"}` | — |
| 18 | GET `/readyz`（免认证） | 200 `{"status":"ready"}` | 503 `not_ready` `retryable:true` |
| 19 | GET `/metrics` | 200 expvar 文本 | 401（有 token 时） |

### events.md 专属（SSE 语义，server.go `handleEvents` + `parseAfter`）

- 帧格式：`id: <seq>\nevent: <event_type>\ndata: <payload_json>\n\n`；心跳 `: ping\n\n` 每 15s
- 断点续传：查询参数 `?after=<seq>` 或请求头 `Last-Event-ID: <seq>`（数字）；从 seq 之后重放
- 运行已终态：只重放历史事件后即关闭；否则进入 follow 循环
- follow：eventshub 推送 + 1s 轮询兜底；**单连接最长 5 分钟**，之后服务端关闭，客户端应带 `Last-Event-ID` 重连
- gap 回填：推送丢失中间 seq 时服务端自动 ListAfter 补齐；**重复投递是有意的、幂等的**——客户端按 `seq` 去重
- 无 Flusher 的客户端（如某些代理后）收到一次性完整重放（非流式）
- `sensitivity` 字段：当前全部为 `"normal"`（预留字段）

### 事件目录（28 常量全量，来源 `domain/event.go` + 发射点 grep）

**已发射（19）**——payload 形状从发射点代码提取：

| 事件 | 发射点 | payload |
|---|---|---|
| `run.created` | RunService.CreateRun | `{"goal":"created"}`（固定值） |
| `run.started` | EinoRunner executeNew / RunChild | `{"text":"run started","tool":""}` |
| `run.resumed` | EinoRunner executeResume | `{"text":"run resumed","tool":""}` |
| `run.finished` | drain 结束 | `{"text":"<最终输出>","tool":""}` |
| `run.failed` | drain/executeResume 出错 | `{"text":"<错误信息>","tool":""}` |
| `run.cancelled` | drain 检测 ctx.Done | `{"text":"<ctx.Err()>","tool":""}` |
| `run.timed_out` | Runtime 超时分支 | `{"text":"run timed out"}` |
| `plan.created` | Runtime plan 模式 | `{"text":"plan with N steps"}` |
| `model.token` | drain 流式回调 | `{"text":"<截至目前累计文本>","tool":""}` |
| `model.completed` | drain 每轮文本完成 | `{"text":"<本轮文本>","tool":""}` |
| `model.failed` | drain | `{"text":"<错误信息>","tool":""}` |
| `tool.completed` | drain 工具输出 | `{"text":"<工具名>","tool":"<工具名>"}` |
| `tool.failed` | drain CodedError | `{"text":"<错误信息>","tool":"<工具名>"}` |
| `budget.consumed` | Runtime 步骤边界 | `{"model_calls":int,"tool_calls":int,"duration_seconds":int}`（累计值） |
| `budget.exceeded` | Runtime 预算终止分支 | `{"text":"budget exceeded: <code>"}`（code ∈ tool_calls/model_calls/output_bytes_budget_exceeded） |
| `approval.requested` | Runtime 审批流 | `{"text":"<工具名> requires approval","approval_id":"...","tool_name":"..."}`；Runner 中断路径为 `{"text":"interrupted","tool":""}` |
| `approval.decided` | RunService.DecideApproval | `{"approved":bool,"approval_id":"...","tool_name":"...","reason":"..."}` |
| `verification.passed` | Runtime 校验通过 | `{"text":"verification passed"}` |
| `verification.failed` | Runtime 校验失败 | `{"text":"verification failed"}` |

**已定义未发射（9）**（当前版本预留，文档中标注"预留"）：`plan.updated`、`step.started`、`step.finished`、`model.requested`、`tool.requested`、`tool.authorized`、`tool.denied`、`tool.started`、`checkpoint.saved`

---

## Chunk 1: 中文 7 篇

### Task 1: `docs/api/overview.md`

**Files:**
- Create: `docs/api/overview.md`

- [ ] **Step 1.1: 写入**。结构（事实全部来自上表「通用」节）：
  1. 基础约定：Base URL `http://localhost:8080`、Content-Type、1 MiB 请求体、时间戳单位
  2. 认证：Bearer Token 语义 + `/healthz` `/readyz` 免认证 + 401 示例
  3. 多租户：`X-Owner-ID` 头 + 缺省 `"default"` + 跨 owner 404
  4. 错误格式：`{code,message,retryable}` + 常见 code 表（bad_request/not_found/conflict/unauthorized/internal/tools_not_configured）
  5. Idempotency-Key 语义（仅 POST /runs）
  6. 预算只紧不松：服务端上限表 + 收紧规则
  7. 文档目录导航表（链接 sessions/runs/events/approvals/settings/system 6 篇）

### Task 2: `docs/api/sessions.md`

**Files:**
- Create: `docs/api/sessions.md`

- [ ] **Step 2.1: 写入**。4 端点各按模板：**方法/路径/认证 → 请求字段表 → 响应字段表 → 状态码 → curl 示例 → 注意事项**。端点 1-4 用上表事实；curl 示例：

```bash
curl -X POST localhost:8080/api/v1/sessions -d '{"title":"demo"}'
curl localhost:8080/api/v1/sessions
curl localhost:8080/api/v1/sessions/<session_id>
curl -X DELETE localhost:8080/api/v1/sessions/<session_id>
```

注意事项：DELETE 409 时需先取消活跃 Run。

### Task 3: `docs/api/runs.md`

**Files:**
- Create: `docs/api/runs.md`

- [ ] **Step 3.1: 写入**。5 端点（list/create/get/cancel/resume）按同模板；create 请求字段表引用 `createRunReq` 5 字段 + `budget` 内嵌 5 字段 + `mode` 取值 `react|plan_execute`（缺省 react）；Run 完整字段表；Idempotency-Key 与 `duplicated` 注意事项；cancel/resume 的 409 语义。

```bash
curl -X POST localhost:8080/api/v1/runs -H 'Idempotency-Key: demo-1' -d '{"session_id":"<session_id>","goal":"分析项目并给出重构建议","mode":"react","budget":{"max_duration_seconds":600,"max_model_calls":30,"max_tool_calls":50,"max_iterations":12}}'
curl 'localhost:8080/api/v1/runs?session_id=<session_id>&limit=20'
curl localhost:8080/api/v1/runs/<run_id>
curl -X POST localhost:8080/api/v1/runs/<run_id>/cancel
curl -X POST localhost:8080/api/v1/runs/<run_id>/resume
```

### Task 4: `docs/api/events.md`

**Files:**
- Create: `docs/api/events.md`

- [ ] **Step 4.1: 写入**。结构：
  1. 端点本体按统一模板（请求参数 `after`、响应 SSE、404）
  2. SSE 连接语义（帧格式/心跳/5 分钟重连/gap 回填/seq 去重/Last-Event-ID 续传）——全部来自「events.md 专属」事实节
  3. **事件目录**：已发射 19 个（三列表格：事件/触发时机/payload）+ 已定义未发射 9 个（一行表格，标注预留）——全部从「事件目录」事实节照抄
  4. `sensitivity` 字段说明
  5. 前端消费建议：按 `(seq)` 去重、按 `event:` 分发

```bash
curl -N localhost:8080/api/v1/runs/<run_id>/events
curl -N 'localhost:8080/api/v1/runs/<run_id>/events?after=42'
curl -N localhost:8080/api/v1/runs/<run_id>/events -H 'Last-Event-ID: 42'
```

### Task 5: `docs/api/approvals.md`

**Files:**
- Create: `docs/api/approvals.md`

- [ ] **Step 5.1: 写入**。2 端点；Approval 完整字段表；decide 的 409 三种来源（已决定/已过期/forbidden）；审批流注意事项（高风险工具 → `waiting_approval` → 批准后恢复执行；拒绝 → Run failed `approval_rejected`）。

```bash
curl localhost:8080/api/v1/runs/<run_id>/approvals
curl -X POST localhost:8080/api/v1/runs/<run_id>/approvals/<approval_id> -d '{"approve":true,"reason":"ok"}'
```

### Task 6: `docs/api/settings.md`

**Files:**
- Create: `docs/api/settings.md`

- [ ] **Step 6.1: 写入**。4 端点；ToolStatus/ToolConfigIn 字段表（含 `status: up|down|error`、`args` omitempty）；model GET/PUT 字段表 + `provider` 仅接受 `openai_compat` + 空串不改 + `api_key` 只写不回显（响应仅 `api_key_set` 布尔）；501 `tools_not_configured` 触发条件。

```bash
curl localhost:8080/api/v1/settings/tools
curl -X PUT localhost:8080/api/v1/settings/tools -d '{"skillsEnabled":true,"mcpEnabled":false,"mcpServers":[]}'
curl localhost:8080/api/v1/settings/model
curl -X PUT localhost:8080/api/v1/settings/model -d '{"model":"gpt-4o-mini","base_url":"https://api.openai.com/v1","api_key":"sk-xxx"}'
```

### Task 7: `docs/api/system.md`

**Files:**
- Create: `docs/api/system.md`

- [ ] **Step 7.1: 写入**。3 端点（healthz/readyz/metrics）；免认证说明；readyz 503 语义；metrics 为 expvar JSON（`go_expvar` 风格键值，含 `runs_started`、`tool_calls_total`、`latency_ms_*` 桶、`event_append_failed`）；expvar 原始格式提示（非 `{code,message,retryable}` 包裹）。

```bash
curl localhost:8080/healthz
curl localhost:8080/readyz
curl localhost:8080/metrics
```

## Chunk 2: 英文镜像 + 验收

### Task 8: `docs/en/api/` 7 篇英文镜像

**Files:**
- Create: `docs/en/api/overview.md`、`sessions.md`、`runs.md`、`events.md`、`approvals.md`、`settings.md`、`system.md`

- [ ] **Step 8.1: 以对应中文定稿为底逐一翻译**。要求：结构/表格行列逐一对应；技术名词保留（`Idempotency-Key`、`Last-Event-ID`、`waiting_approval`、`expvar`、事件类型字面量、字段名）；导航链接指向同目录英文文件；curl 示例与中文版完全一致。

- [ ] **Step 8.2: 校验**。

Run:
```bash
ls docs/api/*.md docs/en/api/*.md | wc -l   # 14
grep -cE '^## ' docs/api/overview.md docs/en/api/overview.md   # 两语标题数一致（逐篇）
```

### Task 9: 验收检查（spec §6）

- [ ] **Step 9.1: 链接检查（零坏链）**

```bash
for f in docs/api/*.md docs/en/api/*.md; do
  d=$(dirname "$f")
  grep -oE '\]\(([^)#]+)' "$f" | sed 's/](//' | grep -vE '^(https?|mailto):' | while read -r t; do
    t=${t%%#*}; [ -z "$t" ] && continue
    [ -e "$d/$t" ] || echo "BROKEN: $f -> $t"
  done
done
```
Expected: 无 `BROKEN`。

- [ ] **Step 9.2: 路由覆盖 19/19（批2 = 端点文档 vs server.go）**

约定：**每篇端点文档的每个端点用二级标题 `## <METHOD> <path>`**（如 `## POST /api/v1/sessions`），保证可统计。

```bash
grep -cE 'HandleFunc|mux.Handle' backend/internal/api/http/server.go   # 19
grep -rhoE '^## (GET|POST|PUT|DELETE) /' docs/api/*.md | sort -u | wc -l   # 19
grep -rhoE '^## (GET|POST|PUT|DELETE) /' docs/en/api/*.md | sort -u | wc -l   # 19
```
Expected: 19/19/19。

- [ ] **Step 9.3: 事件目录 28/28 对齐 event.go**

```bash
grep -cE 'EventType = "' backend/internal/domain/event.go   # 28
grep -cE '^\| `[a-z]+\.[a-z_]+`' docs/api/events.md          # 28
grep -cE '^\| `[a-z]+\.[a-z_]+`' docs/en/api/events.md       # 28
```
Expected: 28/28/28。

- [ ] **Step 9.4: curl 实跑（无需模型 Key）**

```bash
cd backend && make build
AGENT_ADDR=:18098 AGENT_DB_PATH=/tmp/rivus-api-smoke.db \
  MODEL_BASE_URL=http://127.0.0.1:9 MODEL_API_KEY=dummy MODEL_NAME=dummy \
  ./bin/agentd >/tmp/rivus-api-smoke.log 2>&1 &
PID=$!
sleep 2
SES=$(curl -sf -X POST localhost:18098/api/v1/sessions -d '{"title":"api-smoke"}' | grep -oE '"session_id":"[^"]+"' | cut -d'"' -f4)
[ -n "$SES" ] && echo "create-session OK"
curl -sf "localhost:18098/api/v1/sessions" | grep -q "$SES" && echo "list-sessions OK"
curl -sf "localhost:18098/api/v1/sessions/$SES" | grep -q '"title":"api-smoke"' && echo "get-session OK"
curl -sf "localhost:18098/api/v1/runs?session_id=$SES" | grep -q '"runs":' && echo "list-runs OK"
curl -sf -o /dev/null localhost:18098/healthz && echo "healthz OK"
curl -sf -o /dev/null localhost:18098/readyz && echo "readyz OK"
curl -sf localhost:18098/metrics | grep -q latency_ms_0_50 && echo "metrics OK"
kill $PID; rm -f /tmp/rivus-api-smoke.db*
```
Expected: 逐行 OK。注意：必须在**同一次 shell 调用**内完成（后台进程跨调用会被回收，批1 教训）；session 响应键为 `session_id`。

- [ ] **Step 9.5: 回归（零代码变更证明）**

```bash
cd backend && go test ./... -count=1 -timeout 600s
cd ../frontend && npx tsc --noEmit && npx vitest run
```
Expected: 后端全包 ok；前端 20 文件 / 127 测试。

- [ ] **Step 9.6: 双语齐全 + Commit**

```bash
for f in overview sessions runs events approvals settings system; do
  [ -f "docs/api/$f.md" ] && [ -f "docs/en/api/$f.md" ] && echo "OK $f" || echo "MISSING $f"
done
git add docs/api docs/en/api
git commit -m "docs(api): per-endpoint API reference with 28-event catalog (zh/en)"
git status --short
```
Expected: 7 × OK；status 除 `.omo/` 干净。

---

## 自审记录

| 检查项 | 结论 |
|---|---|
| 覆盖 spec 批2 全部要点（7 篇 × 2 语、统一模板、28 事件目录、SSE 语义、sensitivity） | ✅ Task 1-8 |
| 全部字段/状态码/payload 从代码提取并内联于「已核实事实」，无凭记忆 | ✅ server.go 全文 + domain/task.go 全文 + 发射点 grep + config 默认值 |
| 事件目录 28/28（19 发射 + 9 预留标注） | ✅ 事实节 + Step 9.3 校验 |
| 验收映射 spec §6：链接(9.1) 路由(9.2) 事件(9.3) curl(9.4) 回归(9.5) 双语(9.6) | ✅ |
| EN 策略：执行期从中文定稿派生（沿用批1 有意偏离，避免计划内嵌双份注定漂移的文本） | ✅ |
| 已知风险：9.2/9.3 的 grep 计数依赖文档行格式；已在步骤内注明"先人工核对格式再修正统计命令" | ⚠️ |
| 已知风险：curl 实跑需同一次 shell（后台进程跨调用回收，批1 教训已写入步骤） | ⚠️ |
