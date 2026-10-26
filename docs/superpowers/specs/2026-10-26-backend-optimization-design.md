# 后端五项优化设计文档 (A-E) — 预算强制 / 工具幂等 / Resume 预算 / SQLite 并发 / 事件与可观测性

- **日期**: 2026-10-26
- **范围**: 后端 `backend/internal/{runtime,tool,store,observability}` + `backend/internal/domain`。五项独立优化，每项为局部小改（1-6 文件），不引入新依赖、不重构架构。
- **状态**: 设计已确认（用户批准 A-E）。
- **上级 spec**: 吸收并取代 `2026-10-23-budget-enforcement-design.md`（该 spec 已写但**未实施**——代码中 `OverBudget` 不存在，`runtime.go:65` 仍是 `_ = tracker`）。两者冲突处以本 spec 为准；其 §测试 的探针回填策略继续沿用。
- **设计原则**: 最小改动；不改公开 API（HTTP 路径 / JSON 协议 / CLI 参数不变）；向后兼容（DB schema 不变，无迁移）；测试先行（每项对应单元/集成测试）。

---

## 概览：五项优化一览

| 代号 | 标题 | 核心文件 | 问题一句 |
|---|---|---|---|
| A | 预算强制执行 | `runtime/budget.go`, `runtime/runtime.go`, `domain/event.go`, `tool/executor.go`, `runtime/runner_eino.go`, `runtime/runner_child.go` | `BudgetTracker` 定义完整却从未被调用检查（`_ = tracker` 死代码），仅 `MaxDurationSeconds` 靠 ctx 超时兜底 |
| B | 工具幂等 Bug 修复 | `tool/executor.go` | `defer e.seen[key]=""` 在首次成功后清空缓存，第 2+ 次幂等调用命中缓存返回空串 |
| C | Resume 预算硬编码 | `runtime/runner_eino.go` | `executeResume` 写死 `{12,30,50,600}`，绕过"只收紧不放宽"（该入口目前是未接线代码，见 §C 现状） |
| D | SQLite 单连接瓶颈 | `store/sqlite.go` | `SetMaxOpenConns(1)/SetMaxIdleConns(1)` 串行化全部读写，WAL 并发读未利用 |
| E | 事件丢失与可观测性 | `runtime/runner_eino.go`, `runtime/runner_child.go`, `runtime/runtime.go`, `observability/metrics.go`, `app/app.go` | emit channel 满时 `default` 丢弃；`Events.Append` 错误被忽略；expvar 仅计数器，无时延分布 |

---

## A — 预算强制执行（让 BudgetTracker 真正生效）

### 现状（已核实）

- `runtime/budget.go` 定义了完整 `BudgetTracker`（`CheckIteration`/`CheckTool`/`Add*`/`Snapshot`，`AddOutput` 在 :55），但 `runtime/runtime.go:64-65` 仅 `tracker := NewBudgetTracker(budget); _ = tracker`，全部方法无人调用。
- 仅 `MaxDurationSeconds` 靠 `context.WithTimeout` 兜底（`runtime.go:66-71`）。`max_tool_calls` / `max_model_calls` / `max_output_bytes` 完全不强制。
- `domain/event.go` 无 `budget.exceeded` 事件类型（全仓 grep 无 `EvtBudgetExceeded`）。
- `RunService.CreateRun` 已用 `ClampByServer` 收紧预算（`run_service.go:56-61`），`runtime.go:112` 已把 `run.Budget` 传入 `RunRequest` —— 预算数据流上游完好，只缺闸门。
- 前置 spec `2026-10-23-budget-enforcement-design.md` 的设计（`OverBudget` 事后闸门、循环接线、`context.Background()` 围栏）**吸收进本 spec**，并扩展 output_bytes 维度 + `budget.exceeded` 事件。

### 方案

**1. `domain/event.go` — 新增事件类型**

```go
EvtBudgetExceeded EventType = "budget.exceeded" // 预算超限终止
```

**2. `runtime/budget.go` — 新增 `OverBudget`**

```go
// OverBudget 事后判断是否已达任一已设上限（>=，放行 N 个即停）。
// limit <= 0（未设）的维度不强制，返回 ("", false)。
// 优先级：tool_calls > model_calls > output_bytes（确定性命中）。
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
```

既有方法（`CheckIteration`/`CheckTool`/`Add*`/`Snapshot`）保留不动（`Check*` 供未来事前闸门用，本 spec 不接）。

**3. `runtime/runtime.go` — 接线事件循环**

- 删 `:65` 的 `_ = tracker`（`tracker` 变活）。
- 事件循环（`:126-142`）：
  - `EvtToolCompleted` → `tracker.AddToolCall()`；`EvtModelCompleted` → `tracker.AddModelCall()` + `tracker.AddOutput(len(ev.Text))`；再 `r.appendBudget(ctx, run.ID, tracker, started)`。
  - 每轮末尾统一 `code, hit := tracker.OverBudget()`；`hit && !budgetFired` → 置 `budgetCode/budgetMsg/budgetFired` + `_ = r.deps.Runner.Cancel(run.ID)`（首次命中一次，幂等）。
- `appendBudget` 改签名读 `tracker.Snapshot()`：payload 三键/行序/取值与现状逐字节一致（`Snapshot().ModelCalls` == 原 `modelCalls`），已合 `TestBudgetConsumed*` 集成测试保持绿。
- 本地计数器 `toolEvidence`/`modelCalls` 删除；三处读值点改 `tracker.Snapshot().ToolCalls`（`:160` modelFailed 判断、`:167` Verify 参数、`:168` 空结果判断——删局部变量后编译器强制自洽）。
- 循环后终态分支：插在 `waitApproval` 块之后、`DeadlineExceeded` 块**之前**：

  ```go
  if budgetCode != "" {
      // 外层 ctx 可能同窗到期已死（database/sql 对死 ctx 静默 no-op）→ 一律 context.Background()
      bgCtx := context.Background()
      _ = r.deps.Tasks.UpdateStatus(bgCtx, ownerID, run.ID, domain.RunFailed, budgetCode, budgetMsg)
      r.append(bgCtx, run.ID, domain.EvtBudgetExceeded, budgetMsg)
      observability.IncrRunFinished(false)
      r.deps.Runner.DeleteCheckpointsByRun(bgCtx, run.ID)
      return
  }
  ```

- 输出截断：Verify 前，若 `budget.MaxOutputBytes > 0 && len(finalText) > budget.MaxOutputBytes`，截断 finalText 并追加 `...[truncated]`。

**4. executor 入口二道防线（`tool/executor.go` + `runtime/runner_eino.go` + `runtime/runner_child.go`）**

- `ExecRequest` 新增字段 `MaxOutputBytes int`（0 = 不限）。
- `executor.Execute` 成功路径在既有 per-tool 截断（`def.MaxResultBytes`，`:84-86`）之后追加：

  ```go
  if req.MaxOutputBytes > 0 && len(out) > req.MaxOutputBytes {
      out = out[:req.MaxOutputBytes] + "...[truncated]"
  }
  ```

- `gatewayTool` 结构体新增 `maxOutputBytes int`，两处 `executor.Execute` 调用（`runner_eino.go:167/:175`）填入 `MaxOutputBytes: g.maxOutputBytes`；`buildAgentFiltered`（`runner_child.go:93`）构造 `gatewayTool` 时传 `budget.MaxOutputBytes`（该函数已持 `budget` 参数）。
- 语义：单次工具输出若超过整个运行的 output 预算，在执行入口即被截断——防单个巨输出未经闸门直接落地。

### 数据流

```
run() 事件循环:
  EvtToolCompleted → tracker.AddToolCall()
  EvtModelCompleted → tracker.AddModelCall() + AddOutput(len(text))
  → 每轮 OverBudget()：命中(usage>=limit) → Runner.Cancel(内层 ctx) + 记 budgetCode
  → Cancel → Eino drain <-ctx.Done() 发 EvtRunCancelled → channel 关闭 → 循环终止
run() 循环后: budgetCode != "" → context.Background():
    UpdateStatus(RunFailed, error_code=budgetCode) + EvtBudgetExceeded + 清 checkpoint → return
→ SSE handleEvents 观察 Status.Terminal() → 收尾；前端 run.error_code 由 AssistantBubble 泛型渲染
```

### 错误 / 边界

| 场景 | 行为 |
|---|---|
| 某维度预算 = 0（未设） | `OverBudget` 该维度跳过（`limit>0` 守卫）→ 向后兼容 |
| 两维度同时满 | 首次命中者决定 `error_code`（tool 先查，确定性）；取消后循环终止，不重复报 |
| `Cancel` 对已自然结束的 run | `cancels.cancel` 找不到 → error 被 `_ =` 吞掉，无副作用 |
| 外层 ctx deadline 同时到 | 预算分支排在 deadline 分支**之前**，且 DB 写全用 `context.Background()`（沿用前置 spec §2(d) 已论证的围栏） |
| 子运行 `RunChild` | 走 `buildAgentFiltered`（gatewayTool 截断生效），但不经 `run()` 循环 → 无 OverBudget 闸门（YAGNI，与前置 spec 一致） |
| 与 `budget.consumed` 事件 | 计数源改 tracker，快照值/行序/三键不变 → 已合集成测试保持绿 |
| executor 截断 vs per-tool 截断 | per-tool `MaxResultBytes` 先（细粒度），budget 截断后（运行级上限），两者独立生效 |

### 测试

- 单测 `backend/internal/runtime/budget_test.go`（新）：`OverBudget` + `Add*` 联动 —— tool 上限命中/未命中、model 上限对称、两维度同满取 tool code、limit=0 不强制、`Snapshot()` 反映 Add。
- 单测（executor 截断，可并入 executor 测试）：`ExecRequest.MaxOutputBytes=10` 且输出 20 字节 → 返回 10 字节 + `...[truncated]`。
- 集成 `backend/tests/integration/budget_cap_test.go`（新）：测试矩阵沿用前置 spec §测试 的探针回填策略 ——
  - tool 上限：`max_tool_calls=1`，脚本模型 `EnqueueToolCall ×2` → 终态 `RunFailed` + `error_code=tool_calls_budget_exceeded`，含 `run.failed`（`tool.completed` 精确条数 plan 阶段探针实测回填）。
  - model 上限：`max_model_calls=1`，`EnqueueToolCall` + `EnqueueText`（工具轮+文本轮=2 次模型调用）→ 终态 `RunFailed` + `error_code=model_calls_budget_exceeded`，含 `run.failed`。
  - 对照组：`max_tool_calls=10` 正常 run → `RunSucceeded`、`error_code` 为空（闸门不误伤）。
- 回归：`go test ./tests/integration/ -run TestBudgetConsumed` 保持绿。

---

## B — 工具幂等 Bug 修复

### 现状（已核实）

`tool/executor.go:56-68`：首次调用 `e.seen[key]` 未命中（`dup=false`），走到 `:64-68` 注册 `defer e.seen[key]=""`；`:91` 成功后写入 `seen[key]=out`，但**函数返回时 defer 执行，把刚写入的真实结果覆盖为空串**。第 2+ 次相同 `(runID, toolCallID)` 命中 `dup=true`，返回 `prev=""` → 幂等工具重复调用永远拿到空字符串。

### 方案（`tool/executor.go`）

幂等分支重写：

```go
if def.Idempotent {
    key := req.RunID + ":" + req.ToolCallID
    e.mu.Lock()
    if prev, dup := e.seen[key]; dup {
        e.mu.Unlock()
        return &ExecResult{Output: prev, OutputSize: len(prev)}, nil // 返回真实上次结果
    }
    e.mu.Unlock()
}
```

- **删除** `defer func(){ e.seen[key]="" }()`（整个清空逻辑移除）。
- 成功路径保留现有缓存写入（`:88-93` `e.seen[key] = out`）。
- 失败路径（`:79-83` return）不触碰 `e.seen` → 首次成功结果保留，失败不污染缓存、重试仍真实执行。

### 不变量

| 调用序列 | 行为 |
|---|---|
| 首次执行成功 | 返回 `out`，缓存 `out` |
| 第 2+ 次相同 key | 命中 → 返回缓存 `out`（真实值，非空），不重复执行 |
| 首次执行失败 | 不写入 `e.seen` → 下次重试真实执行 |

### 测试

`backend/internal/tool/executor_test.go`（新/增补）：幂等工具连续两次相同 `(runID, toolCallID)` 调用 → 两次 `Output` 相等且非空、handler 只被调 1 次（计数断言）；失败后重试 → handler 再次被调（不返回缓存）。

---

## C — Resume 预算硬编码修复

### 现状（已核实，含重要修正）

- `runner_eino.go:45-49` `ResumeRequest` **无** `Budget` 字段；`:230` `executeResume` 写死 `domain.Budget{MaxIterations: 12, MaxModelCalls: 30, MaxToolCalls: 50, MaxDurationSeconds: 600}`。
- **重要**：`EinoRunner.Resume`/`executeResume` 目前**没有任何调用方**（全仓 grep：`AgentRunner` 接口仅定义、未被当作类型使用；唯一 `.Resume(` 调用是 HTTP → `RunService.Resume` → `rt.Execute` → `Runtime.run` → `Runner.Run` 即 `executeNew`，该路径在 `runtime.go:112` 已正确传 `run.Budget`）。
- 即：**生产 Resume 路径预算正确**；写死预算的是未接线的 checkpoint-resume 入口。若未来接线（真正的 checkpoint 断点续跑），会绕过"只收紧不放宽"。

### 方案（仅 `runtime/runner_eino.go`，1 文件）

1. `ResumeRequest` 新增字段：

```go
type ResumeRequest struct {
    RunID         string
    CheckpointID  string
    ApprovedTools map[string]bool
    Budget        domain.Budget // Resume 继承 Run 实际预算（只收紧不放宽）
}
```

2. `executeResume` 用 `req.Budget` 替代写死常量，全零回退硬编码默认（向后兼容）：

```go
budget := req.Budget
if budget == (domain.Budget{}) {
    budget = domain.Budget{MaxIterations: 12, MaxModelCalls: 30, MaxToolCalls: 50, MaxDurationSeconds: 600}
}
agent, err := r.buildAgent(ctx, req.RunID, budget, req.ApprovedTools)
```

3. **`service/run_service.go` 不改**（该文件从不调用 `runner.Resume`；其路径已携带 `run.Budget`）。未来接线 `EinoRunner.Resume` 的调用方从 `domain.Run.Budget` 填充 —— 本 spec 只硬化入口，不发明新调用（YAGNI）。

### 测试

单测 `backend/internal/runtime/runner_eino_test.go`（新/增补）：
- 传入 `Budget{MaxIterations: 3, ...}` → 构造的 agent 使用 3（非写死 12）。
- 传入全零 `Budget{}` → 回退默认（12/30/50/600）。
- 实现途径：把 budget 解析提为小纯函数直接单测，或经 `buildAgent` 注入断言 —— plan 阶段选实现成本更低者。

---

## D — SQLite 单连接瓶颈

### 现状（已核实，含修正）

- `store/sqlite.go:22-23`：`SetMaxOpenConns(1)/SetMaxIdleConns(1)`，全部读写串行，WAL 并发读未利用。
- **调用方共 5 处**（初稿漏列）：`app/app.go:50`（文件路径）、`tests/testutil/harness.go:193`、`tests/bench/bench_test.go:19`、`internal/model/factory_test.go:22/:42`（均 `:memory:`）。
- **方案取舍**：批准方向的 `max(1, WorkerConcurrency)` —— 默认 `WorkerConcurrency=1`（`config.Default`）时等于 1，是**无效改动**；故采用批准方向的另一选项"固定 8-16"（取 8）。
- **`:memory:` 陷阱**：SQLite 的 `:memory:` 是**每连接独立的内存库**。若对 `:memory:` 提升连接数，池中不同连接会看到不同的空库 → `no such table`。必须保持单连接。

### 方案（仅 `store/sqlite.go`，1 文件；**签名不变**，5 个调用方零改动）

```go
func Open(path string) (*sql.DB, error) {
    ...
    // 连接池：文件库走 WAL 并发读 → 提到 8；:memory: 每连接是独立内存库，
    // 多连接会分库（no such table），必须单连接。
    conns := 8
    if path == ":memory:" || strings.Contains(path, "mode=memory") {
        conns = 1
    }
    db.SetMaxOpenConns(conns)
    db.SetMaxIdleConns(conns)
    db.SetConnMaxLifetime(time.Minute * 10)
    ...
}
```

- pragmas（WAL / `busy_timeout=5000` / `foreign_keys=ON` / `synchronous=NORMAL`）不变。
- `migrate` 全部操作经 `tx`（事务内共享单连接），与池大小无关；**更新两处过时注释**（`:55`、`:74` 中"MaxOpenConns(1) 死锁"的表述改为准确说法）。
- 无新依赖、无 schema 变更。

### 并发安全

WAL：多读者 + 单写者；`busy_timeout=5000` 处理写写冲突。`migrate` 启动时单执行，无并发风险。`path == ":memory:"` 覆盖现有全部测试调用；`mode=memory` DSN 分支兜底其他内存形态。

### 测试

- 集成 `backend/tests/integration/sqlite_concurrency_test.go`（新）：**用 `t.TempDir()` 文件库**（`:memory:` 单连接无并发意义），N=8 goroutine 并发 `CreateRun`/`Events.Append`/`GetRun`，断言无死锁、无 `database is locked` 超时、写入可读。
- 现有 3 个 `:memory:` 调用方保持绿 —— 单连接路径行为与现状完全一致。

---

## E — 事件丢失与可观测性

### 现状（已核实）

1. `runner_eino.go:341-350` `emit`：`out` 容量 64，`select` 带 `default:` → 满时**静默丢弃**。`emit` 调用点共 10 处：`drain` 内 7 处、`executeNew`/`executeResume` goroutine 2 处、`runner_child.go:122`（`RunChild`，有 `ctx`）——初稿漏了 `runner_child.go`。
2. `runtime/runtime.go`：`append`/`appendBudget`/`appendApproval` 及 `New` 的 OnEvent 回调，`Events.Append` 错误全被 `_, _ =` 忽略（`:37/:191/:204/:214`）。
3. `observability/metrics.go`：仅 expvar 计数器；`latSumMs/latCount` 累加但**从无人读取**（写后即弃），无时延分布导出。

### 方案

**1. `runner_eino.go` + `runner_child.go` — `emit` 阻塞式发送（带 ctx 退出）**

```go
func (r *EinoRunner) emit(ctx context.Context, out chan<- RuntimeEvent, runID string, typ domain.EventType, text, tool, payload string) {
    ev := RuntimeEvent{Type: typ, Text: text, ToolName: tool, Payload: payload, RunID: runID}
    select {
    case out <- ev:
    case <-ctx.Done():
        return // 运行已取消/超时，放弃压入已满通道，避免永久阻塞
    }
    if r.onEvent != nil {
        r.onEvent(ev)
    }
}
```

- 全部 10 处 `r.emit(out, ...)` 改为 `r.emit(ctx, out, ...)`（调用点均有 `ctx` 在作用域）。
- 通道容量保持 64（阻塞 + ctx 分支已足够）。
- 死锁面：消费者（`Runtime.run` / `RunChild`）均 `for ev := range ch` 读到关闭为止，不提前退出 → 生产者不会永久阻塞；取消路径由 `ctx.Done()` 兜底。

**2. `runtime/runtime.go` — `Append` 错误日志 + 计数**

- `Deps` 新增 `Log *slog.Logger`；`New()` 内 nil 回退 `slog.Default()`（**`tests/testutil/harness.go:259` 构造 Deps 未带 Log，必须 nil 安全**）；`app.go:80` 传 `a.log`（获得 LogLevel 控制）。
- `append`/`appendBudget`/`appendApproval` 及 `New` 的 OnEvent 回调：`Append` 返回 err 时

  ```go
  if _, err := r.deps.Events.Append(...); err != nil {
      r.deps.Log.Warn("event append failed", "run_id", runID, "type", typ, "err", err)
      observability.IncrEventAppendFailed()
  }
  ```

- `observability/metrics.go` 新增 `event_append_failed` expvar 计数器 + `IncrEventAppendFailed()`。

**3. `observability/metrics.go` — 时延直方图（50ms 桶）**

```go
var (
    latB0_50    = expvar.NewInt("latency_ms_0_50")
    latB50_100  = expvar.NewInt("latency_ms_50_100")
    latB100_200 = expvar.NewInt("latency_ms_100_200")
    latB200_500 = expvar.NewInt("latency_ms_200_500")
    latB500Plus = expvar.NewInt("latency_ms_500_plus")
)

// ObserveLatencyMs 按 50ms 桶累计时延分布（/debug/vars 可见，外部抓取后可推 P50/P95 近似）。
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

- `ObserveTool` 末尾调用 `ObserveLatencyMs(latencyMs)`。
- 既有 `latSumMs/latCount`（写而不读）**不动**（死代码清理是独立 follow-up）。

### 测试

- 单测 `backend/internal/observability/metrics_test.go`（新）：`ObserveLatencyMs` 边界值（0/49/50/99/199/499/500/大值）落入正确桶。
- 单测（emit）：满 channel 阻塞发送 + `ctx` 取消 → emit 返回不卡死（复用 `runner_stream_test.go` 夹具风格）。
- 回归：既有 SSE/stream 集成测试保持绿（事件序列完整）。

---

## 变更文件清单（按优化项）

| 优化 | 文件 | 改动 |
|---|---|---|
| A | `domain/event.go` | 新增 `EvtBudgetExceeded` |
| A | `runtime/budget.go` | 新增 `OverBudget()` |
| A | `runtime/runtime.go` | 接线 tracker、`appendBudget` 读 snapshot、`AddOutput`、预算终态分支、finalText 截断 |
| A | `tool/executor.go` | `ExecRequest.MaxOutputBytes` + 入口截断 |
| A | `runtime/runner_eino.go` | `gatewayTool.maxOutputBytes` 字段 + 两处 Execute 调用传入 |
| A | `runtime/runner_child.go` | `buildAgentFiltered` 构造 gatewayTool 时传 `budget.MaxOutputBytes` |
| B | `tool/executor.go` | 移除 `defer e.seen[key]=""`，命中返回真实 `out`，失败不写入 |
| C | `runtime/runner_eino.go` | `ResumeRequest.Budget` 字段 + `executeResume` 使用（全零回退默认） |
| D | `store/sqlite.go` | `Open` 内连接数 8（`:memory:` 保持 1），更新两处过时注释；**签名不变** |
| E | `runtime/runner_eino.go` | `emit` 加 ctx，阻塞式发送；9 处调用点传 ctx |
| E | `runtime/runner_child.go` | `emit` 调用点传 ctx（:122） |
| E | `runtime/runtime.go` | `Deps.Log`（nil→`slog.Default()`）；`append*` 错误 slog + `IncrEventAppendFailed` |
| E | `observability/metrics.go` | `event_append_failed` 计数器 + `latency_ms_*` 5 桶直方图 + `ObserveLatencyMs` |
| E | `app/app.go` | `runtime.New` 传 `a.log`（1 行） |

> `store.Open` 调用方 5 处（app、testutil、bench、factory_test×2）因**签名不变**而零改动。

### 测试文件（新）

- `backend/internal/runtime/budget_test.go`
- `backend/internal/tool/executor_test.go`（幂等 + 预算截断）
- `backend/internal/runtime/runner_eino_test.go`（Resume 预算解析）
- `backend/internal/observability/metrics_test.go`
- `backend/tests/integration/budget_cap_test.go`
- `backend/tests/integration/sqlite_concurrency_test.go`

---

## 风险与对策

| 风险 | 对策 |
|---|---|
| `emit` 改阻塞后消费者停读则生产者卡死 | 消费者均读到 channel 关闭才退出；`ctx.Done()` 分支兜底取消路径；测试覆盖"满通道+取消" |
| `onEvent`（Events.Append，busy_timeout 最长 5s）同步执行拖慢 drain | 现状即如此（非阻塞 send 后同样同步回调），不新增风险；阻塞只改变"丢事件"行为 |
| `Deps.Log` 零值 nil panic | `New()` 内 nil→`slog.Default()`；harness 构造点无需改 |
| `:memory:` 多连接分库 | `Open` 内识别 `:memory:`/`mode=memory` 强制 1 连接；3 个测试调用方行为与现状一致 |
| 固定 8 连接 vs WorkerConcurrency=1 默认 | 读来自 HTTP handler + worker，8 连接是安全余量；WAL 读并发、写由 busy_timeout 串行化 |
| 预算分支与 deadline 分支同窗竞争 | 预算分支在前且 DB 写全用 `context.Background()`（沿用前置 spec §2(d) 论证） |
| `appendBudget` 改 snapshot 影响已合测试 | 快照值/键序与原局部计数逐字节一致，`TestBudgetConsumed*` 回归兜底 |
| C 的入口当前未接线 | spec 如实标注；低成本硬化，不发明新调用方（YAGNI） |

---

## 明确不做（YAGNI）

- 不新增独立终态 `RunBudgetExceeded`（复用 `RunFailed` + `error_code`，前端 `TERMINAL_*` 零改动）。
- 不改 `model` 层事前严格闸门（A 是事后路线 + 入口截断）。
- 不强制子运行 `RunChild` 的 OverBudget 闸门（其 gatewayTool 截断仍生效）。
- 不给 `EinoRunner.Resume` 发明生产调用方（C 只硬化入口）。
- 不清理 `latSumMs/latCount` 写而不读的死代码（独立 follow-up）。
- 不接线 `CheckIteration`/`CheckTool`（Eino 的 `MaxIterations` 已管 iterations；事前闸门是独立需求）。
- 前端零改动（`AssistantBubble` 已按 `run.error_code` 泛型渲染；`budget.exceeded` 走 SSE `run.failed` 收尾）。

---

## 验收标准

1. `go build ./...` 及 `go vet ./...` 通过。
2. 全量 `go test ./backend/...` 绿（含新增 6 个测试文件）。
3. 预算超限终止：run 终态 `RunFailed` + 正确 `error_code`；`budget.exceeded` 事件存在。
4. 幂等：两次相同幂等调用返回相同非空输出、handler 只执行一次；失败不污染缓存。
5. Resume 预算：`executeResume` 用 `req.Budget`；全零回退默认。
6. SQLite：`Open(":memory:")` 仍单连接（现有测试绿）；文件库 8 连接并发读写无死锁、无 `database is locked`。
7. 事件不丢失：emit 满通道阻塞 + ctx 取消可退出；`Events.Append` 故障有 slog 日志 + `event_append_failed` 递增。
8. `/debug/vars` 出现 `latency_ms_*` 5 桶时延直方图。
9. 前端 `npx tsc --noEmit` + `npx vitest run` 无新增失败（前端零改动）。
