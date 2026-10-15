# timed_out 状态修正 — 设计文档

- **日期**: 2026-10-15
- **范围**: 后端 `backend/internal/runtime/runtime.go`、`backend/internal/domain/event.go`；前端 5 处各 1 行 + `AssistantBubble` 橙色行 + 测试。
- **状态**: 设计已确认。
- **上级 spec**: `docs/superpowers/specs/2026-10-14-frontend-ux-polish-design.md` §5（`timed_out` 行当时因不可达被砍）；本 spec 兑现 `2026-10-14-follow-ups.md` §2。

## 背景 / 缺陷

`RunTimedOut`（`domain/task.go:14`）在状态机有定义、前端全套支持（`runStatusClasses`/`TERMINAL_RUN_STATUSES`/`TERMINAL_STATUS`/badge），但**无任何后端路径产出它**。

真正超时走 `runtime.go:69` 的 `context.WithTimeout`（deadline = `MaxDurationSeconds`，缺省 600s）。deadline 到期后：
- `drain`（`runner_eino.go:263`）走 `ctx.Done()` 分支，发 `EvtRunCancelled`（text=`"context deadline exceeded"`）。
- `run()` 的事件循环（`runtime.go:118-131`）**不处理** `EvtRunCancelled`，继续落到 verify 分支 → 被写成 `verification_failed` 或 `model_failed`。

即：**超时的 run 被误标为"验证失败"**。`budget.go` 里 `CheckIteration`（返回 `ErrTimeout`）是死代码，无人调用。

## 修复

### 后端

1. **`domain/event.go`**：新增事件常量 `EvtRunTimedOut EventType = "run.timed_out"`。
2. **`runtime.go run()`**：事件循环（`for ev := range ch`）结束后、`waitApproval` 判定之后、verify 分支之前，插入：

```go
// 关键：此刻 ctx.Err()==DeadlineExceeded（已过期），database/sql 会对该 ctx
// 短路所有写操作（prepareContext 先返回 ctx.Err()，不落库），若用 ctx 写库则
// UpdateStatus/事件 Append 全部静默失败，run 永远停在 running —— 修复本身 no-op。
// 故所有写库走 context.Background()。
if errors.Is(ctx.Err(), context.DeadlineExceeded) {
    bgCtx := context.Background()
    _ = r.deps.Tasks.UpdateStatus(bgCtx, ownerID, run.ID, domain.RunTimedOut, "timed_out", "run exceeded max duration budget")
    r.append(bgCtx, run.ID, domain.EvtRunTimedOut, "run timed out")
    observability.IncrRunFinished(false)
    r.deps.Runner.DeleteCheckpointsByRun(bgCtx, run.ID)
    return
}
```

插入点选在 `if waitApproval || needApproval.Load()` 块之后（该块命中则已 return，不会误判为超时），verify 之前。

**精确性约束**：
- 用户 Stop 走 `ctx.Err() == context.Canceled`（`run_service.go:86` `runner.Cancel` + 取消注册表），**不是** `DeadlineExceeded`，不会被误判成 timed_out。
- 用 `errors.Is(ctx.Err(), context.DeadlineExceeded)` 而非直接判 `ctx.Err() != nil`，以区分 cancel 与 timeout。
- `runtime.go` 需新增 import `"errors"`（现未 import；`context` 已在）。超时分支的写库**必须**用 `context.Background()`，不能用已过期的 `ctx`（见上代码块注释）。

3. **前端 5 处各 1 行**（`run.timed_out` 进终态/刷新集）：
   - `frontend/src/lib/types.ts`：`EventType` union 加 `'run.timed_out'`。
   - `frontend/src/store/run-store.ts:36`：`TERMINAL_EVENTS` 加 `'run.timed_out'`（`REFRESH_EVENTS` 靠 `...TERMINAL_EVENTS` 自动带上）。
   - `frontend/src/lib/sse-client.ts:17`：`TERMINAL_EVENTS` 加 `'run.timed_out'`（`TERMINAL_STATUS` 已含 `timed_out`）。
   - `frontend/src/lib/status.ts` `eventSummary`：加 `case 'run.timed_out': return 'run timed out'`；`eventCategory` 归入 `'run'`。
   - `frontend/src/components/Messages/AssistantBubble.tsx`：新增 `error_code === 'timed_out'` 分支 → 橙色单行 "Run timed out"（与 cancelled 中性行同构，但用 orange token；有 result 时同 cancelled 的 partial-result 处理）。

### 明确不做（YAGNI）
- 不动 `budget.go` 的 `CheckIteration`/`ErrTimeout` 死代码（本 spec 不引入其使用；清理留作独立债）。
- 不动 `runner_eino.go` drain 的 `EvtRunCancelled` 发射（它仍正确地表达"ctx 结束"，由 `run()` 层区分 cancel vs timeout）。

## 数据流

```
deadline 到期 → drain 发 EvtRunCancelled(ctx 结束信号)
  → run() 事件循环结束
  → errors.Is(ctx.Err(), DeadlineExceeded) 命中
  → 用 `context.Background()` 写：UpdateStatus(RunTimedOut,"timed_out","…") + 发 EvtRunTimedOut + 清 checkpoint + return
  → 前端: run.timed_out 进 TERMINAL/REFRESH → getRun 刷新 → status=timed_out
  → running 气泡徽章变 orange "timed out"；AssistantBubble 橙色 "Run timed out" 行
```

## 错误处理 / 边界

| 场景 | 行为 |
|---|---|
| 正常 deadline 到期 | `RunTimedOut` + `run.timed_out` 事件 |
| 用户 Stop（cancel） | 仍走既有 `run.cancelled`（`run_service.go:90`），不进 timed_out |
| 审批中断（waitApproval 先 return） | 不受影响，不进 timed_out 分支 |
| 部分结果 | 本 spec 超时分支**不**调 `SetResult`，故 timed_out run 的 `result_json` 恒空、无 partial 可展示；`AssistantBubble` 仅显示橙色 "Run timed out" 行 |

## 测试

- **后端集成**（`tests/integration/`，复用 `testutil.Harness`）：造一个 `MaxDurationSeconds` 极小（如 1s）且模型阻塞的 run——用 `ScriptedModel` 的 `EnqueueFunc`（`harness.go` 提供，可挂起 fn 并 sleep）阻塞模型，使其在 1s deadline 内到期；断言终态为 `domain.RunTimedOut`、`error_code == "timed_out"`、事件流含 `run.timed_out`，且终态经 `context.Background()` 写库（非过期 ctx）。
- **前端**：`AssistantBubble.test.tsx` 加 `error_code="timed_out"` 无 result → 橙色 "Run timed out" 行；`status.test.ts` 加 `run.timed_out` → `eventSummary === 'run timed out'`；`run-store`/`sse-client` 终态集包含 `run.timed_out`。

## 验收

- 超时 run 终态为 `timed_out`（非 `verification_failed`/`model_failed`）；事件流含 `run.timed_out`。
- 用户 cancel 仍为 `cancelled`（不回归）。
- 前端 `run.timed_out` 进终态/刷新集 + `AssistantBubble` 橙色行；`npx tsc --noEmit` + `npx vitest run` 全绿。
- 后端 `go test ./...` + `go vet ./...` 全绿。
