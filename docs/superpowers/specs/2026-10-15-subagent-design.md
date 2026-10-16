# Subagent（子 agent 同步委派）— 设计文档

- **日期**: 2026-10-15
- **范围**: 纯后端。新增 `internal/subagent`（`task` 工具 + handler）、`internal/runtime`（`TaskContext` + `RunChild` + `buildAgent` 工具过滤）、`internal/store`（`parent_run_id` 列 + 迁移 + `ListChildrenByRun`）、`internal/domain`（`Run.ParentRunID`）。前端零改动。
- **状态**: 设计已确认（方案 A + 推荐项：同步 `task` 工具、父选工具子集、完整子 Run、只读子、子预算 40% 收紧 + 绝对上限、只落 `parent_run_id` 不加 `/children` 端点）。
- **上级 spec**: 本 spec 是 opencode 式「自治能力」分解中的第一块（Subagent）。模式（build/plan/ask）与权限档（工具分级 + 记住审批）为后续独立 spec。

## 背景 / 定位

对齐 opencode 的「委派子 agent」能力，但落在 rivus-agent 既有形态上：一个 **同步 `task` 工具**，让父 run 的 ReAct 循环把一个子目标委派给一个**工具面受父选择限定的只读子 agent**，子 agent 跑成一条**完整、可审计的独立 `domain.Run`**（DB 行 + 事件流 + 预算 + checkpoint），结果作为工具输出字符串回传父模型。

**已锁定的五个决策（本 spec 全部兑现，不超出）：**
1. 主线 = Subagent（非模式、非权限档）。
2. 子 agent 工具面 = 父**可选子集**，且**限定只读**（`ApprovalNever` + `RiskRead`）。
3. 驱动模型 = **同步阻塞**（父槽位内内联驱动，无异步句柄/fan-out）。
4. 子 run 持久化 = **完整**（DB 行 + checkpoint + 事件 + 预算；审计留 `parent_run_id`）。
5. 子预算 = 父预算 **40%** 收紧 + 命名绝对上限（防 run 预算被吃穿）。

## 关键约束（架构由它决定）

`Execute`（`runtime.go:48`）先抢 worker 池槽位 `r.sem`（`WORKER_CONCURRENCY` 默认 **1**），父 `run()`（`runtime.go:60`）全程持有它。若 `task` 工具内部再走 `Execute(child)`，子的 `select { case r.sem <- struct{}{} }`（`runtime.go:49`）会阻塞在**父仍持有的唯一槽位**上 → **死锁**。

**结论：子 run 在父已持有的槽位内内联驱动（`RunChild` 不碰 `r.sem`），接收父的 `ctx`（cancel/deadline 级联）。** 这是本 spec 的核心不变量。

## 改动

### 1. `internal/domain/task.go` — `Run.ParentRunID`
- `domain.Run` 加字段 `ParentRunID string `json:"parent_run_id,omitempty"``（空 = 顶层 run）。

### 2. `internal/store/schema.sql` + `internal/store/sqlite.go` — `parent_run_id` 列（幂等迁移）
- **`schema.sql` 的 `CREATE TABLE runs` 保持不变**（v1 DDL 字节稳定，不动现有库）。
- `sqlite.go` 的 `migrate`（现只跑 `schemaSQL` + 记 version 1）扩展为**版本化迁移**：
  - 新增 v2：`ALTER TABLE runs ADD COLUMN parent_run_id TEXT`（列可空）。
  - 幂等：对每个未 applied 的版本号，先查 `PRAGMA table_info(runs)` 是否已有 `parent_run_id`——新库（v1 后走 v2 前无该列）执行 ALTER；已有该列（重复开库）则跳过。`schema_migrations(version, applied_at)` 记录 applied。
  - 现有 `agent.db` / `tests/` 的 `:memory:` 均幂等通过。

### 3. `internal/store/task_repo.go` — 读写 `parent_run_id`
- `CreateRun`（`task_repo.go:123` 的 INSERT）加 `parent_run_id` 列（顶层省略 = `NULL`，子传父 ID）。
- `GetRun`（`task_repo.go:155` 的 SELECT/Scan）加 `parent_run_id` 到扫描。
- 新增 `ListChildrenByRun(ctx, parentRunID string) ([]domain.Run, error)`：`SELECT ... FROM runs WHERE parent_run_id = ? ORDER BY created_at ASC`（**测试用**，非 HTTP 端点）。

### 4. `internal/runtime/taskctx.go`（新建）— 父身份注入
```go
type TaskContext struct {
    OwnerID, ParentRunID, SessionID string
    ParentBudget domain.Budget
    Depth        int // 0 = 顶层（可 spawn 子）；>=1 = 子（禁用 task）
}
func WithTaskContext(ctx context.Context, tc TaskContext) context.Context
func TaskContextFrom(ctx context.Context) (TaskContext, bool)
```

**构造点在 `Runtime.Execute`/`run()`（`runtime.go:48/60`），不在 `executeNew`**——只有那里拿得到完整的 `*domain.Run`（含 `SessionID`、`Budget`；`RunRequest` 无 `SessionID`，子行建库时 `runs.session_id` 非空 + 外键）。顶层注入（在 `r.run` 工具循环启动前）：
```go
ctx = WithTaskContext(ctx, TaskContext{OwnerID: ownerID, ParentRunID: run.ID,
    SessionID: run.SessionID, ParentBudget: run.Budget, Depth: 0})
```
值经 `cancels.track` 的 `context.WithCancel`（保留 values）传入 `runner.Run` 的 ctx → 每个工具调用（含 `task`，`gatewayTool.InvokableRun`）都能 `TaskContextFrom`。`SetNeedApprovalHandler` 是按 runID 的 map，与 ctx 正交，不受影响。

### 5. `internal/runtime` — `buildAgent` 工具过滤 + `RunChild`
- `buildAgent`（`runner_eino.go:172`）当前把 `registry.List()` 全量包成 `gatewayTool`。改为接收**工具名过滤 + 深度守卫**：
  ```go
  func (r *EinoRunner) buildAgentFiltered(ctx, runID string, budget domain.Budget,
      approved map[string]bool, toolNames []string, depth int) (*adk.ChatModelAgent, error)
  ```
  - `toolNames` 为 nil → 全量（顶层现状不变）。
  - 非 nil → 只包 `registry.Get(name)` 命中的工具。
  - **深度守卫**：`depth >= 1` 时**强制剔除 `task`**（子不可再 spawn，硬顶 depth=1，防无限嵌套）。
  - `buildAgent`（原签名）改为调 `buildAgentFiltered(ctx, runID, budget, approved, nil, 0)`——**顶层行为零变化**。
- 新增同步驱动方法（实现 `ChildExecutor` 接口）：
  ```go
  type ChildRequest struct {
      OwnerID, ChildRunID string
      Goal string
      ToolNames []string
      Budget domain.Budget
      Depth int
      CheckpointID string
  }
  type ChildExecutor interface {
      RunChild(ctx context.Context, req ChildRequest) (result string, status domain.RunStatus, err error)
  }
  func (r *EinoRunner) RunChild(ctx context.Context, req ChildRequest) (string, domain.RunStatus, error)
  ```
  `RunChild`（不碰 `r.sem`，复用 `r.modelFactory/registry/executor/checkpoints`；**`RunChild` 是子行的唯一终态化者**，handler 只拼工具输出串，无 double-finalization）：
  0. `tasks.MarkStarted(ctx, req.OwnerID, req.ChildRunID)`（queued→running，`runtime.go:62` 同款。**必须**：`UpdateStatus` 走 `ValidTransition(queued,…)` 只能到 running/cancelled，缺了它任何终态写都 `ErrConflict` 被 `_ =` 吞掉，子行卡死在 queued）。
  1. `buildAgentFiltered(ctx, req.ChildRunID, req.Budget, nil, req.ToolNames, req.Depth)`（`approved=nil`：只读子无需预批）。
  2. `adk.NewRunner` + **同步** drain（复用 `r.drain` 语义，同步收集 `finalText`/`toolEvidence`/`modelFailed`/`failed`，参照 `runtime.go:117-134`）。
  3. 终态化（**不注册 needApproval handler、不做 plan/steps**——只读子永不停审批；按序判定，命中即写终态 + `IncrRunFinished` + 清 ckpt）：
     - **cancel**（`ctx.Err()==context.Canceled`，父 cancel 级联）→ **`context.Background()` 写库**（dead-ctx 不变量，同 timed-out 修复）：`UpdateStatus(childID, RunCancelled, "cancelled")` + `IncrRunFinished(false)`。
     - **deadline**（`errors.Is(ctx.Err(), context.DeadlineExceeded)`；父 deadline 到期会先于/等于子的子预算触发，父 ctx 主导）→ `context.Background()`：`UpdateStatus(childID, RunTimedOut, "timed_out", …)`（`runtime.go:140-151` 同款）+ `IncrRunFinished(false)`。
     - **pre-drain 错误**（`buildAgentFiltered`/`adk.NewRunner` 失败）→ 参照 `runtime.go:112-115`：`UpdateStatus(childID, RunFailed, "runner_error", …)` + `IncrRunFinished(false)`，不搁浅子行。
     - **`EvtApprovalRequested`（防御；只读子理论不可达——子工具全 `ApprovalNever`，`CheckPolicy` 只对 Always/(Conditional∧write∨external) 触审批；唯一理论触发是 adk `Action.Interrupted`，纯 ReAct 子不产生）** → `UpdateStatus(childID, RunFailed, "child_needs_approval_not_supported_in_v1")` + `IncrRunFinished(false)`。
     - `modelFailed && toolEvidence==0` → `UpdateStatus(childID, RunFailed, "model_failed", …)` + `IncrRunFinished(false)`。
     - 否则 `Verify` + `SetResult(childID, rb)` + `UpdateStatus(childID, Succeeded/failed, "verification_failed"?)` + `IncrRunFinished`（`runtime.go:159-178` 同款；**非 cancel/deadline 路径 `ctx` 仍有效，这些写走活的 `ctx`**）。
  4. 返回 `(finalText, 终态, err)` 给 `task` handler。

### 6. `internal/subagent`（新建）— `task` 工具 + handler
`task` 工具定义（`Approval: ApprovalNever`、`Idempotent: false`、`Risk: RiskRead`，risk 由子的只读面界定）：
```json
InputSchema: {
  "type":"object",
  "properties":{
    "goal":{"type":"string"},
    "tools":{"type":"array","items":{"type":"string"}},
    "max_model_calls":{"type":"integer"},
    "max_tool_calls":{"type":"integer"},
    "max_iterations":{"type":"integer"}
  },
  "required":["goal"]
}
```
- `ToolNames`（可选）：父为子挑选的**只读**工具子集；缺省 = 父全部 `ApprovalNever` 只读工具。
- 子预算入参（可选）：经 `clampChildBudget`（见下）收紧；缺省 = 40% 父预算 + 绝对上限。

`NewTaskHandler(reg *tool.Registry, tasks *store.TaskRepo, runner runtime.ChildExecutor) tool.Handler`：
1. `tc, ok := TaskContextFrom(ctx)`；`!ok || tc.Depth >= 1` → 返回 error 工具输出（深度闸，双保险：buildAgent 已剔除 task，这里再防）。
2. `goal` 非空校验；`tools` 白名单校验：每个 name 在 `reg`（`reg.Get`）且 `Approval==ApprovalNever && Risk==RiskRead`，**任一是 write/external 即拒绝**（强制只读，防 LLM 传入 `workspace_write`）；缺省（未传 `tools`）= `reg.List()` 里全部 `ApprovalNever && RiskRead` 工具（**含 `task` 本身**——read/never，靠 `buildAgentFiltered` 的 depth 守卫剔除，此处不特判）。
3. `childBudget := clampChildBudget(tc.ParentBudget, 入参可选值)`。
4. `childID := uuid.NewString()`；`tasks.CreateRun(ctx, &domain.Run{ID:childID, OwnerID:tc.OwnerID, SessionID:tc.SessionID, Status:RunQueued, Mode:"react", Goal:goal, Budget:childBudget, ParentRunID:tc.ParentRunID, Attempt:1})`（子行落库，可审计；`Attempt:1` 显式，覆盖 DDL DEFAULT）。
5. `result, status, err := runner.RunChild(ctx, ChildRequest{OwnerID:tc.OwnerID, ChildRunID:childID, Goal:goal, ToolNames:toolNames, Budget:childBudget, Depth:tc.Depth+1, CheckpointID:"ckpt_"+childID+"_"+nano})`。
6. 返回给父模型的 JSON 字符串：`{"child_run_id":..., "status":..., "result":..., "error"?}`。

`clampChildBudget`（命名常量，本 spec 定死 40% + 上限；配置化留 follow-up）：
```go
const (
    childBudgetFrac          = 0.40
    childCapModelCalls       = 20
    childCapToolCalls        = 30
    childCapIterations       = 10
    childCapDurationSeconds  = 180
    childCapOutputBytes      = 8 * 1024
)
// 每个字段 = min(入参可选值或父40%, 绝对上限)；用 domain.Budget.ClampByServer 语义（收紧不放宽）。
```

### 7. `internal/app/app.go` — 装配
- `builtin.RegisterAll` 之后：`registry.Register(subagent.TaskTool(registry, tasks, runner))`（`TaskTool(reg, tasks, runner)` 返回 `tool.Definition`，其 `Handler` 捕获共享单例 `registry`+`TaskRepo`+`*EinoRunner`）。
- `runner` 已实现 `runtime.ChildExecutor`（`RunChild`），无循环依赖：`subagent → runtime,store,tool,domain`；`runtime` 不 import `subagent`；`app` 组装两者。

## 数据流（一次 task 调用）

```
父 run() 持有唯一 worker 槽位（runtime.go:48）
  → 父模型调 task{goal, tools:[file_read,knowledge_search], ...}
    → subagent handler:
        TaskContextFrom(ctx) 拿父身份/预算/depth=0
        校验 tools 全为 ApprovalNever+RiskRead（否则 error，不 spawn）
        childBudget = clampChildBudget(父预算, 入参)
        TaskRepo.CreateRun(子行: parent_run_id=父.ID, owner/session=父, status=queued, budget=childBudget)
        runner.RunChild(父ctx, {childID, goal, toolNames, childBudget, depth=1, ckpt}):
            buildAgentFiltered(depth=1 剔除 task, 工具面=toolNames)
            adk.NewRunner + 同步 drain（复用 run() 的终态化: verify/SetResult/UpdateStatus/清ckpt）
            子事件走子 runID（run.started → … → run.finished/failed/timed_out）
        返回 {child_run_id, status, result} 给父模型
  → 父继续 ReAct 循环（槽位全程未让出，无死锁）
```

## 边界 / 错误处理

| 场景 | 行为 |
|---|---|
| 父 cancel | 父 `ctx`（经 `cancels.track`）取消 → 子 `RunChild` 收同 ctx → 子 drain 走 `EvtRunCancelled`，子行落 `cancelled`；`task` 工具向父模型返回 `error` 终态输出，父继续 |
| 子 deadline 到 | 子 ctx `DeadlineExceeded` → 子走 `run.timed_out` 分支（**`context.Background()` 写库**，同 timed-out 修复不变量）→ 子行落 `timed_out`；task 返回 `{status:"timed_out"}` |
| 子 `model_failed` 且无 tool evidence | 子落 `failed`（`model_failed`）；task 返回 `result` 空 + `error` 摘要，父模型自行决定重试 |
| 子 verify 失败 | 子落 `verification_failed`；同上 |
| 入参 `tools` 含 write/external 工具 | handler 白名单拒绝，task 直接返回 error 工具输出（**不建子行**） |
| 子意外触审批（只读面下理论不可达，防御） | `RunChild` 不注册 needApproval handler；若 `ErrNeedsApproval` 冒到 → 子落 `failed`（`child_needs_approval_not_supported_in_v1`），task 返回 error |
| 父预算耗尽 | `clampChildBudget` 保证子预算 ≤ 父 40%（且 ≤ 绝对上限），父总预算不被子吃穿 |
| depth≥1 | `buildAgentFiltered` 剔除 `task` + handler `tc.Depth>=1` 双闸 → 无无限嵌套 |

## 测试

- **harness 接线（前置）**：`testutil.New` 自建 registry（`builtin.RegisterAll` + `extraTools`）但**从不注册 `task` 工具**（`app.go` 的装配被绕过），且 `Harness` 不暴露 `Runner` 字段——故父 `EnqueueToolCall("task",…)` 会命中 "unknown tool"。须在 `testutil.New` 里镜像 `app.go:§7` 注册 `subagent.TaskTool(registry, h.Tasks, runner)`，使 `task` 进入 harness registry 且 handler 捕获该 harness 的 `TaskRepo`/`*EinoRunner`。这是集成测试能跑通的前提。
- **后端集成**（`tests/integration/subagent_test.go`，复用 `testutil.Harness`；`h.Model` 用 `EnqueueToolCall("tc","task",{goal,tools})` 让父模型委派）：
  - **委派成功**：`ScriptedModel` 使父出 `task` 工具调用、子出 final 证据文本 → 断言：子 Run 行存在（`parent_run_id`=父.ID、status 终态）、父模型收到子 `result` 文本、事件流含子 runID 的 `run.started`/`run.finished`（`h.Events.ListAfter`）。
  - **只读不变量**：入参 `tools` 含 `workspace_write` → `task` 工具返回 error，**`ListChildrenByRun` 为空**（不建子行）。
  - **递归闸**：depth=1 子的工具面不含 `task`（断言 `buildAgentFiltered` 过滤结果；或给子 EnqueueToolCall `task` 应得到"未知工具"）。
  - **预算收紧**：子 `Run.Budget` 各字段 ≤ 父 40% 且 ≤ 绝对上限。
  - **cancel**：父 cancel 时子亦 `cancelled`（若 harness 支持；否则退化为断言子行终态非 running）。
  - 迁移幂等（**用文件库，非 `:memory:`**——fresh `:memory:` 是全新 schema，证明不了 `PRAGMA table_info` 守卫）：`store` 测试用 `t.TempDir()` 里的文件 db，`Open` 两次（第二次对已含 `parent_run_id` 的库再 Open），断言列存在且**不报** "duplicate column"。
- **前端**：无（本 spec 纯后端；`domain.Run.ParentRunID` 序列化进既有 `GET /runs/{id}` 响应即可，无端点/组件新增）。
- 全绿：`cd backend && go build ./... && go vet ./... && go test ./...`。

## 明确不做（YAGNI / follow-up）

- **异步并行子**（fan-out 句柄 + 子任务表 + 父 resume/轮询）→ 独立 spec（本 spec 是它的前置：子已是完整 Run，未来可改 `RunChild` 为池内 `Execute` + 注入结果事件）。
- **写工具子 + 审批冒泡 / 父 checkpoint 携带 in-flight** → 独立 spec（需先引入 exec/shell 工具 + 权限档 spec）。
- 多级嵌套（depth>1）→ 不做（硬顶 depth=1）。
- `GET /runs/{id}/children` 端点 / 前端子 run 树状可视化 → 不做（仅落 `parent_run_id` 数据 + `ListChildrenByRun` 仓储方法）。
- 子专属成本统计 / 预算可配置 → 不做（40% + 命名常量，配置化留后续）。

## 验收

- 父 run 经 `task` 工具委派一个只读子 run：子为**独立可审计 `domain.Run` 行**（`parent_run_id`=父、事件流、终态、预算收紧），结果文本作为工具输出回传父模型，父 ReAct 循环继续。
- **无死锁**：`WORKER_CONCURRENCY=1` 下父槽位全程持有，子内联驱动；`go test ./...` 全绿。
- 只读不变量（子只读工具面）、递归闸（depth=1 无 `task`）、预算收紧（子 ≤ 父 40% + 上限）均有测试锁定。
- `go build ./...` + `go vet ./...` + `go test ./...` 全绿；前端零改动（`git diff` 不含 `frontend/`）。
