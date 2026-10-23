# 预算强制 — 让 BudgetTracker 真正生效 — 设计文档

- **日期**: 2026-10-23
- **范围**: 后端 `runtime`/`budget`。把死代码 `BudgetTracker` 接成**事后强制闸门**:运行中观察到 `max_tool_calls` / `max_model_calls` 被用满即取消 runner,run 落到终态 `RunFailed` + 新 `error_code`。前端零改动(复用 `AssistantBubble` 对 `run.error_code` 的泛型渲染)。
- **状态**: 设计已确认。
- **上级 spec**: 承接 `2026-10-22-budget-consumed-design.md` §明确不做里的"不做预算强制(`BudgetTracker` 仍不动——强制是独立需求)"。本 spec 兑现该 follow-up。

## 现状 / 路线取舍(关键)

预算五个维度里,现状只有两个真正被强制:

| 维度 | 现状 |
|---|---|
| `max_iterations` | ✅ Eino `AgentConfig.MaxIterations`(生成循环上限,Eino 内置) |
| `max_duration_seconds` | ✅ `run()` 顶部 ctx timeout → `RunTimedOut`(`runtime.go:143-153`) |
| `max_tool_calls` | ❌ 未强制(`gatewayTool.InvokableRun` 无计数闸门) |
| `max_model_calls` | ❌ 未强制(仅当比 `max_iterations` 更小时才有实际效果) |
| `max_output_bytes` | ❌ 未强制(`drainStream` 有 200KB/条 硬顶,但非预算语义) |

`runtime.go:64-65` 是死代码:`tracker := NewBudgetTracker(budget)` 后紧接 `_ = tracker` —— `BudgetTracker` 的 `Check*`/`Add*`/`Snapshot` 全无人调用。

本 spec 选**事后强制 + tracker 单事实源**:

- 在 `run()` 的事件循环里,把"数 tool/model 完成"从本地计数器改为 `tracker.AddToolCall()` / `tracker.AddModelCall()`(tracker 变活,唯一事实源,删掉 `_ = tracker`)。
- 每轮事件后问一次 `tracker.OverBudget()`;首次命中即 `r.deps.Runner.Cancel(run.ID)`(复用 Stop 按钮的 cancel 路径——`RunService.Cancel` 底层就是它)+ 记 `budgetCode`/`budgetMsg`。
- 循环结束后(Verify 之前)若 `budgetCode != ""` → `UpdateStatus(RunFailed, budgetCode, ...)` + 发一条 `EvtRunFailed` + 清 checkpoint + return。
- **判断语义**:`usage.X >= limit.X` 即命中(观察到第 N 个完成即停,正好放行 N 个)。`limit <= 0`(未设)则该维度不强制,行为与现状完全一致(向后兼容)。
- **不改终态词汇**:复用 `RunFailed` + `error_code`,不新增 `RunBudgetExceeded` 状态/事件(前端 `status.ts`/`poller.ts` 的 `TERMINAL_*` 集合零改动)。

## 改动

### 1. `backend/internal/runtime/budget.go` — 新增 `OverBudget`

现有 `BudgetTracker`(`:18-26`)保留,新增(贴 `CheckTool` 旁):

```go
// OverBudget 事后判断是否已达任一已设上限（>=，放行 N 个即停）。
// 命中返回 (code, true)，code 为要写入 run.error_code 的稳定值；未设上限
// （limit<=0）的维度不强制，返回 ("", false)。tool 先查（命中即停，
// 两维度同时满只会发生在已取消之后，不会同时被检查到）。
func (b *BudgetTracker) OverBudget() (code string, hit bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit.MaxToolCalls > 0 && b.usage.ToolCalls >= b.limit.MaxToolCalls {
		return "tool_calls_budget_exceeded", true
	}
	if b.limit.MaxModelCalls > 0 && b.usage.ModelCalls >= b.limit.MaxModelCalls {
		return "model_calls_budget_exceeded", true
	}
	return "", false
}
```

（改后 `Add*Call` 在循环里被调用；`Snapshot` 供 `appendBudget` 读值。`Check*`/`Iterations` 仍保持——Eino 管 iteration，不在本 spec 内强制。）

### 2. `backend/internal/runtime/runtime.go` — 接线

**(a) 删死代码**：`run()` 顶部 `:65` 的 `_ = tracker` 删除（`tracker` 变活）。

**(b) 循环改用 tracker + 后置闸门**：现状 `:120-145`：

```go
	toolEvidence := 0
	modelCalls := 0
	started := time.Now()
	finalText := ""
	waitApproval := false
	modelFailed := false
	for ev := range ch {
		switch ev.Type {
		case domain.EvtToolCompleted:
			toolEvidence++
			r.appendBudget(ctx, run.ID, modelCalls, toolEvidence, started)
		case domain.EvtApprovalRequested:
			waitApproval = true
		case domain.EvtModelCompleted:
			finalText = ev.Text
			modelCalls++
			r.appendBudget(ctx, run.ID, modelCalls, toolEvidence, started)
		case domain.EvtModelFailed:
			modelFailed = true
		case domain.EvtRunFailed:
			finalText = ev.Text
		}
	}
```

改为（`toolEvidence`/`modelCalls` 本地计数器删除；计数与读值全走 tracker；每轮末尾统一查一次闸门）：

```go
	started := time.Now()
	finalText := ""
	waitApproval := false
	modelFailed := false
	var budgetCode, budgetMsg string
	var budgetFired bool
	for ev := range ch {
		switch ev.Type {
		case domain.EvtToolCompleted:
			tracker.AddToolCall()
			r.appendBudget(ctx, run.ID, tracker, started)
		case domain.EvtApprovalRequested:
			waitApproval = true
		case domain.EvtModelCompleted:
			finalText = ev.Text
			tracker.AddModelCall()
			r.appendBudget(ctx, run.ID, tracker, started)
		case domain.EvtModelFailed:
			modelFailed = true
		case domain.EvtRunFailed:
			finalText = ev.Text
		}
		// 事后强制：达任一已设上限即取消 runner（首次命中一次），落到下方 RunFailed。
		if code, hit := tracker.OverBudget(); hit && !budgetFired {
			budgetFired = true
			budgetCode = code
			budgetMsg = "run stopped: " + code
			_ = r.deps.Runner.Cancel(run.ID)
		}
	}
```

**要点**：
- `appendBudget` 改签名读 tracker snapshot（见 (c)）——发出的 `budget.consumed` 值/行序/三键**与已合版本逐字节一致**（`tracker.Snapshot().ModelCalls`==原 `modelCalls`，`.ToolCalls`==原 `toolEvidence`），已合的 `TestBudgetConsumed*` 集成测试保持绿。
- 闸门在 switch 后统一查（而非各 case 内重复）：DRY；`OverBudget` 幂等，首次命中后置 `budgetFired` 且 `Cancel` 后 channel 关闭、循环自然终止，不会二次取消。
- `Cancel` 用的是 EinoRunner 内层 ctx（`cancels.track` 包装的那层），外层 `ctx`（deadline+taskctx）通常仍活；但为避免"外层 deadline 恰在同一窗口到期"导致 database/sql 静默 no-op（与 timed_out 分支同理），循环后预算分支的 **DB 写全部用 `context.Background()`**（见 §2(d)）。

**(c) `appendBudget` 读 snapshot**：现状 `:198-205` 改为：

```go
func (r *Runtime) appendBudget(ctx context.Context, runID string, tracker *BudgetTracker, started time.Time) {
	sn := tracker.Snapshot()
	payload, _ := json.Marshal(map[string]int{
		"model_calls":      sn.ModelCalls,
		"tool_calls":       sn.ToolCalls,
		"duration_seconds": int(time.Since(started).Seconds()),
	})
	_, _ = r.deps.Events.Append(ctx, runID, domain.EvtBudgetConsumed, string(payload), "normal")
}
```

**(d) 循环后映射终态**：在 `if waitApproval || needApproval.Load() {...}` 块之后、`if errors.Is(ctx.Err(), context.DeadlineExceeded) {...}` 之前，插：
```go

	if budgetCode != "" {
		// 与 timed_out 分支同理：若外层 deadline 恰在同一窗口到期，ctx 可能已死，
		// database/sql 会静默 no-op 这些写、run 永远停在 running —— 故 DB 写全部用 context.Background()。
		bgCtx := context.Background()
		_ = r.deps.Tasks.UpdateStatus(bgCtx, ownerID, run.ID, domain.RunFailed, budgetCode, budgetMsg)
		r.append(bgCtx, run.ID, domain.EvtRunFailed, budgetMsg)
		observability.IncrRunFinished(false)
		r.deps.Runner.DeleteCheckpointsByRun(bgCtx, run.ID)
		return
	}
```

**(e) 下游读值改 tracker**：本地计数器 `toolEvidence`/`modelCalls` 已删，三处读值点全改 `tracker.Snapshot().ToolCalls`（循环内删 `toolEvidence := 0`/`modelCalls := 0` 声明）：`modelFailed && toolEvidence == 0`（:160）→ `modelFailed && tracker.Snapshot().ToolCalls == 0`；`Verify(run.SuccessCriteria, finalText, toolEvidence)`（:167）→ `Verify(..., tracker.Snapshot().ToolCalls)`；空结果检查 `report.Passed && len(run.SuccessCriteria) == 0 && toolEvidence == 0 && finalText == ""`（:168）→ `... && tracker.Snapshot().ToolCalls == 0 && ...`。（删局部变量后 :168 不编译，编译器会强制把三处全改到，自洽。）

## 数据流

```
run() 事件循环: EvtToolCompleted→tracker.AddToolCall / EvtModelCompleted→tracker.AddModelCall
  → 每轮 OverBudget()：命中(usage>=limit) → Runner.Cancel(内层 ctx) + 记 budgetCode
  → Cancel → EinoRunner.drain 的 <-ctx.Done() 分支发 EvtRunCancelled（Stop 机制 byproduct，§3=a 保留）→ channel 关闭 → 循环终止
run() 循环后: budgetCode != "" → UpdateStatus(RunFailed, error_code=budgetCode) + EvtRunFailed + 清 checkpoint → return
→ SSE: handleEvents ticker/push 观察 Status.Terminal() → 收尾；前端 run.error_code 由 AssistantBubble 泛型渲染
```

## 错误 / 边界

| 场景 | 行为 |
|---|---|
| `max_tool_calls=0`/`max_model_calls=0`（未设上限） | `OverBudget` 该维度跳过（`limit>0` 守卫）→ 不取消，run 照旧（向后兼容） |
| 两维度同时满 | 首次命中的那个（tool 先查）决定 `error_code`；取消后循环终止，不会同时被报 |
| 取消后 drain 发 `run.cancelled` | 保留（§3=a 决策）：它是 Stop 机制 byproduct，SSE 收尾照常；`run.cancelled`/`run.failed` 均已在前端 `TERMINAL_EVENTS`，无副作用 |
| `Cancel` 对已自然结束的 run | `cancels.cancel` 找不到已 unregister 的 run → 返回 error，被 `_ =` 吞掉，无副作用 |
| 外层 ctx deadline 同时到 | 预算分支在 deadline 分支之前判断；若同时，`budgetCode` 先命中。预算分支 DB 写用 `context.Background()`（§2d），外层 ctx 死活都照写，不会静默 no-op 卡死在 running |
| 子运行（RunChild） | 不走 `run()` 循环 → 不受本 spec 影响（YAGNI，见下） |
| 与 budget.consumed 事件 | 计数源改 tracker，但快照值/行序/三键不变 → 已合集成测试保持绿 |

## 测试

### 后端单测 `backend/internal/runtime/budget_test.go`（新）
直接构造 `BudgetTracker`（不设 runner）测 `OverBudget` + `Add*` 联动：
- `limit.MaxToolCalls=1`，`AddToolCall` 1 次 → `OverBudget()==("tool_calls_budget_exceeded", true)`；0 次 → 未命中。
- `limit.MaxModelCalls=1` 对称。
- 两维度均设 1、两个各 Add 1 次 → 命中且 code 为 `tool_calls_budget_exceeded`（tool 先查，确定性）。
- `limit=0`（未设）→ 任意 Add 后 `OverBudget()==("", false)`（不强制）。
- `Snapshot()` 反映 Add 后的 ModelCalls/ToolCalls。

### 后端集成 `backend/tests/integration/budget_cap_test.go`（新，复用 `testutil` 单-attempt 夹具，DB 断言）
- **tool 上限**：`CreateRun` payload 覆写 `budget.max_tool_calls=1`（其余字段保留），脚本模型 `EnqueueToolCall` ×2（`knowledge_search`，注册表内置）。断言：run 终态 `RunFailed` + `error_code=tool_calls_budget_exceeded`；`agent_events` 里 `tool.completed` **恰 1 条**（第 2 个未完成即被停）；含一条 `run.failed`。
- **model 上限**：`budget.max_model_calls=1`，脚本模型 `EnqueueToolCall`（`knowledge_search`）+ `EnqueueText`（工具轮 + 文本轮 = 自然 2 次模型调用；**不可**用 `EnqueueText`×2 —— 纯文本响应即终止 agent 循环，只会 1 次模型调用，超限无法被观测）。断言：终态 `RunFailed` + `error_code=model_calls_budget_exceeded`；`model.completed` **>= 1**（至少触发了超限的那次模型调用）；含一条 `run.failed`。
  > **不 pin 精确交错数**：cancel 与 Eino 内部 tool 执行的交错（`tool.completed` 究竟 0 还是 1 条）依赖 Eino 循环在 cancel 后是否还推进，属实现细节，纯推理无法确定。plan 阶段用一次性探针实测后回填精确值再写死；spec 层只守护"超限必停 + 正确 error_code"这一核心不变量，避免把未验证假设写进 spec。
- **对照组（不超限）**：同一 run 但 `max_tool_calls=10`（fixture 默认），脚本模型 1 次 `knowledge_search`。断言：终态 `RunSucceeded`、`error_code` 为空（证明闸门不误伤在限内的 run）。

（断言走 `h.GetRun`/`h.Events.ListAfter`，非 SSE 时序，故不用 `openStream`/`waitLine`；`testutil.New` 无 hub 即可——持久化与 hub 无关。）

### 回归（已有测试保持绿）
- `go test ./tests/integration/ -run TestBudgetConsumed` —— `appendBudget` 改读 snapshot 后值不变，钉死序列 `(1,0)→(1,1)→(2,1)` 仍成立。
- 全量 `go build/vet/test ./...` + 前端 `npx tsc --noEmit` + `npx vitest run`（前端无改动，应全绿）。

## 明确不做（YAGNI）

- **不新增独立终态** `RunBudgetExceeded`（不动 domain 状态机 / SSE `TERMINAL_*` / 前端终态集合）；复用 `RunFailed` + `error_code`。
- **不强制 `max_output_bytes`**（需累计流文本量，且 `drainStream` 已有 200KB/条 硬顶；留作独立 follow-up）。
- **不在本 spec 内强制子运行（`RunChild`）的预算**（其 `clampChildBudget` 已按父预算分比例；子运行强制是独立需求）。
- **不改** `gatewayTool`/`model` 层做"事前"严格闸门（本 spec 是"事后"路线，已选定）。
- **不发** 预算触发专用事件（复用 `run.failed`；那条 drain 的 `run.cancelled` byproduct 保留，§3=a）。
- **前端零改动**：`AssistantBubble` 已按 `run.error_code` 泛型渲染失败原因；新 code（`*_budget_exceeded`）落在既有 `if (run.error_code)` 分支内。
