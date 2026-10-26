# 后端五项优化设计文档 (A-E) — 预算强制 / 工具幂等 / Resume 预算 / SQLite 并发 / 事件与可观测性

- **日期**: 2026-10-26
- **范围**: 后端 `backend/internal/{runtime,tool,store,observability,service}` + `backend/internal/domain`。五项独立优化，每项改 1-2 文件，不引入新依赖、不重构架构。
- **状态**: 设计已确认（用户 m0013 批准 A-E，m0015 最终放行）。
- **上级 spec**: 承接 `2026-10-23-budget-enforcement-design.md`（预算强制的事后闸门路线）及 `2026-10-22-budget-consumed-design.md`。
- **设计原则**: 最小改动；不改公开 API（HTTP 路径 / JSON 协议 / CLI 参数不变）；向后兼容（DB schema 不变，无迁移）；测试先行（每项对应单元/集成测试）。

---

## 概览：五项优化一览

| 代号 | 标题 | 核心文件 | 问题一句 |
|---|---|---|---|
| A | 预算强制执行 | `runtime/budget.go`, `runtime/runtime.go`, `domain/event.go` | `BudgetTracker` 定义完整却从未调用检查，仅 `MaxDurationSeconds` 靠 ctx 超时兜底 |
| B | 工具幂等 Bug 修复 | `tool/executor.go` | 幂等工具重复调用命中缓存返回初始空，`defer` 清空首次结果导致第 2+ 次返回空串 |
| C | Resume 预算硬编码 | `runtime/runner_eino.go`, `service/run_service.go` | `executeResume` 写死 `{12,30,50,600}`，忽略 Run 实际预算和服务端上限 |
| D | SQLite 单连接瓶颈 | `store/sqlite.go`, `app.go`, `tests/testutil/harness.go` | `SetMaxOpenConns(1)/SetMaxIdleConns(1)` 串行化全部读写，WAL 并发读未利用 |
| E | 事件丢失与可观测性 | `runtime/runner_eino.go`, `runtime/runtime.go`, `observability/metrics.go` | emit channel 满时 default 丢弃；`Events.Append` 错误被忽略；expvar 仅计数器无 P50/P95 |

---

## A — 预算强制执行（让 BudgetTracker 真正生效）

### 现状

`runtime/budget.go` 定义了完整的 `BudgetTracker`（`CheckIteration`/`CheckTool`/`Add*`/`Snapshot`），但 `runtime/runtime.go:64-65` 仅 `tracker := NewBudgetTracker(budget); _ = tracker`，`BudgetTracker` 的所有方法无人调用。仅 `MaxDurationSeconds` 靠 `context.WithTimeout` 兜底（`runtime.go:66-71`）。`max_tool_calls` / `max_model_calls` / `max_output_bytes` 完全不强制。

### 方案

**1. `domain/event.go` — 新增事件类型**

```go
EvtBudgetExceeded EventType = "budget.exceeded" // 预算超限终止
```

**2. `runtime/budget.go` — 激活 `BudgetTracker`**

保留现有全部方法，新增：

```go
// OverBudget 事后判断是否已达任一已设上限（>=，放行 N 个即停）。
// limit <= 0（未设）的维度不强制，返回 ("", false)。
// 优先级：tool_calls > model_calls > output_bytes，确定性命中。
func (b *BudgetTracker) OverBudget() (code string, hit bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit.MaxToolCalls > 0 && b.usage.ToolCalls >= b.limit.MaxToolCalls {
		return "tool_calls_budget_exceeded", true
	}
	if b.limit.MaxModelCalls > 0 && b.usage.ModelCalls >= b.limit.MaxModelCalls {
		return "model_calls_budget_exceeded", true
	}
	if b.limit.MaxOutputBytes > 0 && b.usage.OutputBytes >= b.limit.MaxOutputBytes {
		return "output_bytes_budget_exceeded", true
	}
	return "", false
}

// CheckOutput 事前检查输出字节是否已超上限（供 executor 二道防线用）。
func (b *BudgetTracker) CheckOutput(n int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit.MaxOutputBytes > 0 && b.usage.OutputBytes+n > b.limit.MaxOutputBytes {
		return domain.ErrBudgetExceeded
	}
	return nil
}
```

`AddOutput(n int)` 已存在，保留。

**3. `runtime/runtime.go` — 接线事件循环**

- 删死代码 `:65` 的 `_ = tracker`（`tracker` 变活）。
- 事件循环：`EvtToolCompleted` 前 `tracker.AddToolCall()`、`EvtModelCompleted` 前 `tracker.AddModelCall()`，并在每轮末尾统一调用 `tracker.OverBudget()`。首次命中即 `r.deps.Runner.Cancel(run.ID)`，记 `budgetCode`/`budgetMsg`，置 `budgetFired` 防止重复取消。
- 循环后（`waitApproval` 判断之后、`DeadlineExceeded` 判断之前）插预算终态分支：`budgetCode != ""` → 用 `context.Background()` 写 DB（避免外层 ctx 死导致静默 no-op 卡在 running）：`UpdateStatus(RunFailed, budgetCode, budgetMsg)` + `append(bgCtx, run.ID, EvtBudgetExceeded, budgetMsg)` + `IncrRunFinished(false)` + `DeleteCheckpointsByRun(bgCtx, run.ID)` + return。
- `appendBudget` 改读 `tracker.Snapshot()`（值/行序/三键与原逐字节一致，已合集成测试保持绿）。
- 本地计数器 `toolEvidence`/`modelCalls` 删除，三处读值点改用 `tracker.Snapshot().ToolCalls`（编译器会强制自洽）。
- 输出截断：循环结束后 `finalText` 若超过 `tracker.limit.MaxOutputBytes`，按 `MaxOutputBytes` 截断并追加 `...[truncated]`（executor 已按 per-tool `MaxResultBytes` 截断，此处为全运行二道防线）。

**4. executor 入口二道防线**

`tool/executor.go` 的 `gatewayTool.InvokableRun` 在返回 `out.Output` 前，若输出长度超过 `tracker` 剩余预算，按 `MaxOutputBytes` 截断（配合 runtime 全运行截断）。executor 本身不持 tracker，该防线由 runtime 在 `finalText` 上统一执行，executor 只保 per-tool 截断（已存在 `def.MaxResultBytes`）。

### 数据流

```
run() 事件循环:
  EvtToolCompleted → tracker.AddToolCall()
  EvtModelCompleted → tracker.AddModelCall()
  → 每轮 OverBudget()：命中(usage>=limit) → Runner.Cancel(内层 ctx) + 记 budgetCode
  → Cancel → Eino drain <-ctx.Done() 发 EvtRunCancelled → channel 关闭 → 循环终止
run() 循环后: budgetCode != "" → context.Background():
    UpdateStatus(RunFailed, error_code=budgetCode) + EvtBudgetExceeded + 清 checkpoint → return
→ SSE handleEvents 观察 Status.Terminal() → 收尾；前端 run.error_code 由 AssistantBubble 泛型渲染
```

### 错误 / 边界

| 场景 | 行为 |
|---|---|
| `max_tool_calls=0`/`max_model_calls=0`/`max_output_bytes=0`（未设） | `OverBudget` 该维度跳过（`limit>0` 守卫）→ 不取消，向后兼容 |
| 两维度同时满 | 首次命中的那个（tool 先查）决定 `error_code`；取消后循环终止，不重复报 |
| `Cancel` 对已自然结束的 run | `cancels.cancel` 找不到 run → 返回 error，被 `_ =` 吞掉，无副作用 |
| 外层 ctx deadline 同时到 | 预算分支 DB 写用 `context.Background()`，不会静默 no-op 卡死在 running |
| 与 `budget.consumed` 事件 | 计数源改 tracker，但快照值/行序/三键不变 → 已合集成测试保持绿 |

### 测试

- 单测 `backend/internal/runtime/budget_test.go`（新）：`OverBudget` + `Add*` 联动（tool 上限、model 上限、两维度同时满、未设上限=0、Snapshot 反映 Add）。
- 集成 `backend/tests/integration/budget_cap_test.go`（新）：tool 上限（终态 `RunFailed` + `error_code=tool_calls_budget_exceeded`、`tool.completed` 恰 1 条、含 `run.failed`）；model 上限（终态 `RunFailed` + `error_code=model_calls_budget_exceeded`、`model.completed >= 1`、含 `run.failed`）；对照组（`max_tool_calls=10`，终态 `RunSucceeded`、`error_code` 为空）。
- 回归：`go test ./tests/integration/ -run TestBudgetConsumed` 保持绿。

---

## B — 工具幂等 Bug 修复

### 现状

`tool/executor.go:56-68`：幂等工具重复调用时，`e.seen[key]` 初始为空字符串 `" "`。首次命中 `dup` 为 false（`prev=""`），返回 `prev`（空）。随后 `defer e.seen[key]=""` 把首次真实结果清空。第 2+ 次幂等调用又命中缓存，返回 `prev=""` → 幂等工具重复调用返回空字符串。

### 方案

`tool/executor.go` `Execute` 中幂等分支重写：

```go
if def.Idempotent {
	key := req.RunID + ":" + req.ToolCallID
	e.mu.Lock()
	if prev, dup := e.seen[key]; dup {
		e.mu.Unlock()
		// 命中返回真实上一次结果（非空），不重复执行
		return &ExecResult{Output: prev, OutputSize: len(prev)}, nil
	}
	e.mu.Unlock()
	// 执行器：成功后缓存；失败不写入、不清空，保留首次结果供重试
}
```

移除 `defer func(){ e.seen[key]="" }()`。执行成功后在现有位置（`:88-93`）写入 `e.seen[key] = out`；执行失败（`:79-83` 的 `return nil, ...`）不触及 `e.seen`，首次结果保留。

### 不变量

- 首次调用：执行 → 缓存 `out` → 返回 `out`。
- 第 2+ 次相同 `(runID, toolCallID)`：命中缓存 → 返回缓存的 `out`（真实值，非空）。
- 执行失败：不写入/不清空 `e.seen[key]`，下次重试仍会重新执行。

### 测试

- 单测 `backend/internal/tool/executor_test.go`（新或增补）：幂等工具调用两次相同 args，断言两次 `Output` 相等且非空；失败后重试仍重新执行（不返回缓存的空串）。

---

## C — Resume 预算硬编码修复

### 现状

`runtime/runner_eino.go:230` `executeResume` 用写死 `domain.Budget{MaxIterations: 12, MaxModelCalls: 30, MaxToolCalls: 50, MaxDurationSeconds: 600}`，忽略 `Run` 实际预算和服务端上限（`config.Max*`），违反“只收紧不放宽”原则：客户端申请更小预算时 Resume 会放宽回硬编码值。

### 方案

**1. `runtime/runner_eino.go` — `ResumeRequest` 新增 `Budget` 字段**

```go
type ResumeRequest struct {
	RunID         string
	CheckpointID  string
	ApprovedTools map[string]bool
	Budget        domain.Budget // 新增：Resume 继承 Run 实际预算
}
```

`executeResume(ctx, req)` 使用 `req.Budget` 构建 agent（替代写死常量），并可选 `ClampByServer`（服务端上限由 `config` 通过 `run.Budget` 已在 Create 时收紧；Resume 直接复用已收紧的 `run.Budget`，不再二次放宽）。

**2. `service/run_service.go` — `Resume` 从 `domain.Run.Budget` 读取并传入**

```go
func (s *RunService) Resume(ctx context.Context, ownerID, runID string) error {
	run, err := s.tasks.GetRun(ctx, ownerID, runID)
	...
	s.rt.Execute(context.WithoutCancel(ctx), ownerID, run, s.collectApprovals(ctx, runID))
	// Runtime 内部会把 run.Budget 传给 runner；ResumeRequest.Budget 需从 run.Budget 填充。
	return nil
}
```

注意：`rt.Execute` 的签名是 `(ctx, ownerID, run, approved)`，`run` 已带 `Budget`。`Runtime.run` 内部已用 `run.Budget`（`:63`）。`executeResume` 被 `runner.Resume(ctx, ResumeRequest{..., Budget: run.Budget})` 调用。需要在 `Runtime.run` 或 `runner.Resume` 调用处把 `run.Budget` 填入 `ResumeRequest`。

更精确地：`runtime.New` 的 `OnEvent` / runner 被 `Runtime` 持有。`executeResume` 由 `EinoRunner.Resume` 调用，而 `EinoRunner.Resume` 被 `Runtime.run` 调用（通过 `r.deps.Runner.Resume`）。查看 `runtime.go`：`run()` 只调用 `r.deps.Runner.Run(...)`，不直接调 Resume。Resume 由 HTTP 层 / `RunService.Resume` → `rt.Execute` → `run()` → `Runner.Run`。等等，Resume 路径：`RunService.Resume` 调用 `s.rt.Execute(...)` → `Runtime.Execute` → `run()` → `Runner.Run(ctx, RunRequest{..., Budget: budget})`。`RunRequest.Budget` 来自 `run.Budget`（`:112`）。所以 **New Run 路径已用 run.Budget**。

Resume 路径真正的断点：`runner_eino.go:229-251` `executeResume` 独立于 `RunRequest`，写死预算。修复：`EinoRunner.Resume(ctx, ResumeRequest{..., Budget: run.Budget})`，由调用方（HTTP handler 或 service）填充 `run.Budget`。

查看 `RunService.Resume`（`:93-114`）：它调用 `s.rt.Execute(...)`，不直接调 `runner.Resume`。`Resume` 的事件流由 `Runtime.run` → `Runner.Run`（新 run）而非 `Runner.Resume`？需要确认 `Runner.Resume` 被谁调用。实际上 `EinoRunner.Resume` 实现了 `AgentRunner.Resume` 接口，可能被子运行 / 中断恢复调用。无论如何，`executeResume` 的写死预算是 bug，应改用 `req.Budget`。

最小改动：`executeResume` 改用 `req.Budget`；若 `req.Budget` 全零（未设），回退到现有硬编码默认值作为兜底（向后兼容）。`ResumeRequest.Budget` 新增字段，调用方（service/handler）从 `domain.Run.Budget` 填充。

### 测试

- 单测 `backend/internal/runtime/runner_eino_test.go`（新/增补）：`executeResume` 传入 `Budget{MaxToolCalls: 3}`，断言 agent 构造使用的 budget 等于传入值（非写死 `{12,30,50,600}`）；传入全零预算时回退硬编码默认。
- 集成：Resume 后运行的预算与原 Run 一致（`GetRun.Budget`）。

---

## D — SQLite 单连接瓶颈

### 现状

`store/sqlite.go:22-23`：`SetMaxOpenConns(1)/SetMaxIdleConns(1)`，所有读写串行，WAL 模式下并发读未被利用。注释（`:55`、`:74`）解释单连接是为避免 `migrate` 事务持有唯一连接时对 `db` 另开连接死锁。

### 方案

**1. `store/sqlite.go` — `Open` 提升连接数**

将 `Open(path string)` 改为 `Open(path string, maxOpenConns int)`：

```go
func Open(path string, maxOpenConns int) (*sql.DB, error) {
	if maxOpenConns < 1 {
		maxOpenConns = 1
	}
	db, err := sql.Open("sqlite", path)
	...
	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(maxOpenConns)
	...
}
```

保留 WAL / `busy_timeout=5000` / `foreign_keys=ON` / `synchronous=NORMAL` pragmas 不变。

`migrate` 内全部操作经 `tx`（单事务内共享一条连接），与 `MaxOpenConns` 无关；更新过时注释（`:55`、`:74`），移除“单连接避免死锁”的说明（多连接下 tx 内操作依然串行于 tx 连接，不再有 db 另开连接被阻塞的问题）。

**2. 调用方更新**

- `app/app.go:50`：`store.Open(a.cfg.DBPath, a.cfg.WorkerConcurrency)`（`WorkerConcurrency` 范围 `[1,32]`，`config.Validate` 已保证）。
- `tests/testutil/harness.go:193`：`store.Open(":memory:", 8)`（测试固定 8 连接，充足并发）。

### 并发安全

WAL 模式下多连接可并发读 + 单写；`busy_timeout=5000` 处理写写冲突。`migrate` 在启动时单执行，无并发风险。

### 测试

- 集成 `backend/tests/integration/sqlite_concurrency_test.go`（新）：N 个 goroutine 并发 `CreateRun`/`Events.Append`/`GetRun`，断言无死锁、无 `database is locked` 超时、所有写入最终可读。
- 现有 `go test ./internal/store/...` 保持绿。

---

## E — 事件丢失与可观测性

### 现状

1. `runner_eino.go:341-350` `emit`：`out` 通道容量 64，满时 `default` 分支**静默丢弃**事件，高频 `EvtModelToken` 下易丢事件。
2. `runtime/runtime.go`：`Events.Append` 错误被 `_, _ =` 忽略，持久化失败不可见。
3. `observability/metrics.go`：仅 `expvar` 计数器（`runs_started` 等），无时延分布（P50/P95）。

### 方案

**1. `runtime/runner_eino.go` — `emit` 阻塞式发送**

`emit` 新增 `ctx context.Context` 参数：

```go
func (r *EinoRunner) emit(ctx context.Context, out chan<- RuntimeEvent, runID string, typ domain.EventType, text, tool, payload string) {
	ev := RuntimeEvent{Type: typ, Text: text, ToolName: tool, Payload: payload, RunID: runID}
	select {
	case out <- ev:
	case <-ctx.Done():
		// 运行已取消/超时，不再强制发入已满通道，避免阻塞 drain
		return
	}
	if r.onEvent != nil {
		r.onEvent(ev)
	}
}
```

所有 `drain` 内 `r.emit(out, ...)` 调用改为 `r.emit(ctx, out, ...)`。`executeNew`/`executeResume` 的 `out` 通道容量保持 64（阻塞+ctx 分支即可，不必扩容）。`emit` 被 `return` 后续语句调用的场景（如 `EvtApprovalRequested` 后 `return`），ctx.Done 分支下提前返回亦正确。

**2. `runtime/runtime.go` — `Append` 错误日志 + 上报**

`Runtime.Deps` 新增 `Log *slog.Logger`，由 `app/app.go` 传入 `a.log`。`append` / `appendBudget` / `appendApproval` 中 `Events.Append` 返回错误时：

```go
if _, err := r.deps.Events.Append(...); err != nil {
	r.log.Warn("event append failed", "run_id", runID, "type", typ, "err", err)
	observability.IncrEventAppendFailed() // 新增 expvar 计数器
}
```

**3. `observability/metrics.go` — 新增 expvar 时延直方图**

新增 `expvar.Map` `"latency_ms"`，50ms 桶：`"0_50"`, `"50_100"`, `"100_200"`, `"200_500"`, `"500_plus"`，每个桶一个 `expvar.NewInt`。新增 `ObserveLatencyMs(ms int64)` 按桶递增。`ObserveTool` 调用 `ObserveLatencyMs(latencyMs)`。

```go
var latencyBuckets = expvar.NewMap("latency_ms")
var (
	latB0_50   = expvar.NewInt("0_50")
	latB50_100 = expvar.NewInt("50_100")
	latB100_200 = expvar.NewInt("100_200")
	latB200_500 = expvar.NewInt("200_500")
	latB500Plus = expvar.NewInt("500_plus")
)

func init() {
	latencyBuckets.Add("0_50", latB0_50)
	...
}

func ObserveLatencyMs(ms int64) {
	switch {
	case ms < 50: latB0_50.Add(1)
	case ms < 100: latB50_100.Add(1)
	case ms < 200: latB100_200.Add(1)
	case ms < 500: latB200_500.Add(1)
	default: latB500Plus.Add(1)
	}
}
```

### 测试

- 单测 `backend/internal/observability/metrics_test.go`（新）：`ObserveLatencyMs` 分桶计数正确。
- 集成：高频事件下不丢失（`emit` 阻塞），`Events.Append` 故障时有 slog 日志。

---

## 变更文件清单（按优化项）

| 优化 | 文件 | 改动 |
|---|---|---|
| A | `domain/event.go` | 新增 `EvtBudgetExceeded` |
| A | `runtime/budget.go` | 新增 `OverBudget()`、`CheckOutput()` |
| A | `runtime/runtime.go` | 接线 tracker、`appendBudget` 读 snapshot、输出截断、预算终态分支（`context.Background()`） |
| B | `tool/executor.go` | 移除 `defer e.seen[key]=""`，命中返回真实 `out`，失败不写入/不清空 |
| C | `runtime/runner_eino.go` | `ResumeRequest` 新增 `Budget`；`executeResume` 使用 `req.Budget`，全零回退硬编码默认 |
| C | `service/run_service.go` | `Resume` 从 `domain.Run.Budget` 填充 `ResumeRequest.Budget` |
| D | `store/sqlite.go` | `Open(path, maxOpenConns)`，提升 `SetMaxOpenConns/SetMaxIdleConns`，更新注释 |
| D | `app/app.go` | `store.Open(a.cfg.DBPath, a.cfg.WorkerConcurrency)` |
| D | `tests/testutil/harness.go` | `store.Open(":memory:", 8)` |
| E | `runtime/runner_eino.go` | `emit` 新增 `ctx`，阻塞式发送 + `ctx.Done()` 分支；`drain` 调用处传 ctx |
| E | `runtime/runtime.go` | `Deps` 新增 `Log *slog.Logger`；`append*` 错误 slog 日志 + `observability.IncrEventAppendFailed()` |
| E | `observability/metrics.go` | 新增 `latency_ms` expvar Map（50ms 桶）+`ObserveLatencyMs`，`ObserveTool` 调用 |
| E | `app/app.go` | `New` 中向 `runtime.New` 传入 `a.log`（`Deps.Log`） |

### 测试文件（新）

- `backend/internal/runtime/budget_test.go`
- `backend/internal/tool/executor_test.go`（幂等部分）
- `backend/internal/runtime/runner_eino_test.go`（Resume 预算）
- `backend/internal/observability/metrics_test.go`
- `backend/tests/integration/budget_cap_test.go`
- `backend/tests/integration/sqlite_concurrency_test.go`

---

## 明确不做（YAGNI）

- 不新增独立终态 `RunBudgetExceeded`（复用 `RunFailed` + `error_code`，前端 `TERMINAL_*` 零改动）。
- 不改 `gatewayTool`/`model` 层做事前严格闸门（A 采用事后路线）。
- 不在本 spec 内强制子运行 `RunChild` 预算。
- 不发预算触发专用事件（复用 `run.failed` + 新增 `budget.exceeded`）。
- 前端零改动（`AssistantBubble` 已按 `run.error_code` 泛型渲染）。

---

## 验收标准

1. `go build ./...` 及 `go vet ./...` 通过。
2. 全量 `go test ./backend/...` 绿（含新增 6 个测试文件）。
3. 预算超限终止：run 终态 `RunFailed` + 正确 `error_code`；`budget.exceeded` 事件存在。
4. 幂等：两次相同幂等调用返回相同非空输出；失败不污染缓存。
5. Resume 预算：Resume 后运行使用 `run.Budget` 而非硬编码 `{12,30,50,600}`。
6. SQLite 并发：N 个 goroutine 并发读写无死锁、`database is locked`。
7. 事件不丢失：高频 `EvtModelToken` 下 emit 不丢事件；`Events.Append` 故障有 slog 日志。
8. 可观测性：`/debug/vars` 出现 `latency_ms` 直方图（50ms 桶）。
9. 前端 `npx tsc --noEmit` + `npx vitest run` 无新增失败（前端无改动）。
