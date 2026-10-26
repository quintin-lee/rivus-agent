# 评估体系（Eval System）设计

日期：2026-10-26 ｜ 轮次：第 4 轮「完善优化」方向 2 ｜ 状态：待用户复审

## 1. 背景与问题

- `backend/tests/evals/` 现有 4 个测试（eval_test.go ×3 + model_error_test.go ×1），本质是 harness 集成测试：验证 Verify 证据机制、plan_execute 建步骤、预算兜底。它们没有场景目录、没有统一评分、没有报告产物、没有独立运行入口。
- CI（.github/workflows/ci.yml）只有 backend / frontend 两个 job，eval 混在 `go test ./...` 里。
- 第 3 轮新增的能力（7 个内置工具、skills、tasks 并行、会话记忆）没有任何行为级回归评估。

## 2. 目标与非目标

**目标**：纯 mock 模型的确定性评估体系——场景目录 + 轻量框架 + 三维度评分（正确性/效率/稳定性）+ JSON 报告 + `make eval` 入口 + CI 独立 evals job（上传报告产物）。

**非目标（YAGNI）**：
- 不做真实 LLM / LLM-judge 模式（Q2 已排除）
- 不做独立 CLI（方案 B 已否决：需把 testutil 从 `*testing.T` 解耦，牵动 20+ 测试文件）
- 不引入任何新依赖；不改产品代码（backend/internal、frontend 零改动）
- 不做延迟/成本模拟评分（mock 模式无意义）

## 3. 框架核心（新文件 `backend/tests/evals/framework.go`）

```go
// 三维度期望
type Expect struct {
    TerminalStatus domain.RunStatus   // 期望终态
    ErrorCode      string             // 期望 error_code（可空；亦支持后缀匹配 *_budget_exceeded）
    ResultContains []string           // ResultJSON 必含子串
    MaxModelCalls  int                // 效率维上限（0=不检查）
    MaxToolCalls   int
    RequiredEvents []domain.EventType // 正确性维：事件存在性
}

type Scenario struct {
    Name, Category  string             // category: "tool" | "agent"
    Goal            string
    SuccessCriteria []string
    Mode            string             // 空 = react
    Budget          map[string]any     // 覆盖 fixture 预算（可空）
    Script          func(h *testutil.Harness) string // 排队 mock 输出，返回幂等键
}

type Result struct {
    Name, Category string
    Correct, Efficient, Stable bool
    Pass bool; Detail string
    ModelCalls, ToolCalls int; DurationMs int64
}
```

- **runner `Run(t, h, s) Result`**：`s.Script(h)` 排队 → `h.CreateRun(session, payload, idemKey)` → `h.WaitTerminal(id, 30s)` → 计数取自 `h.Model.Calls()` 与 `h.Events.ListAfter` 事件流 → 三维度判定：
  - 正确性：终态匹配 + 错误码匹配 + ResultContains 全命中 + RequiredEvents 全存在
  - 效率：ModelCalls ≤ MaxModelCalls（>0 时）且 ToolCalls ≤ MaxToolCalls（>0 时）
  - 稳定性：在 30s 时限内到达终态、未 panic
  - 判定失败维度 → `t.Errorf`（CI 门禁），同时 `record(r)` 收集
- **报告**：包级收集器（mutex 保护）；新增 `TestMain(m)`：`m.Run()` 后统一写 `backend/eval-report.json`：

```json
{
  "summary": { "total": 15, "passed": 15, "pass_rate": 1.0 },
  "by_category": { "tool": { "total": 8, "passed": 8 }, "agent": { "total": 7, "passed": 7 } },
  "results": [ { "name": "...", "category": "tool", "correct": true, "efficient": true,
                 "stable": true, "pass": true, "detail": "", "model_calls": 2,
                 "tool_calls": 1, "duration_ms": 12 } ]
}
```

- `.gitignore` 增加 `backend/eval-report.json`。
- 现有 4 个测试保留不动（不迁入框架，避免无谓重构）。

## 4. 场景目录（≥15 个）

`scenarios_tool_test.go`（category=tool，8 个）：

| # | 场景 | 断言要点 | 维度覆盖 |
|---|---|---|---|
| 1 | file_read_write_roundtrip | workspace_write→file_read 内容一致 | 正确性 |
| 2 | file_search_hit | 临时 workspace 造文件→命中 | 正确性 |
| 3 | file_edit_approval_flow | waiting_approval→h.Do 批准→succeeded | 正确性+稳定性 |
| 4 | http_fetch_allowlist_deny | 空 allowlist→tool failed | 正确性 |
| 5 | knowledge_search_hit | 临时 knowledge 目录 fixture→命中 | 正确性 |
| 6 | memory_roundtrip | run1 memory_write→run2 memory_read 复述 | 正确性 |
| 7 | invoke_skill_load | 返回 SKILL.md 正文 | 正确性 |
| 8 | tasks_batch_order | 2 子任务、结果输入序、子 run 终态 | 正确性+效率 |

`scenarios_agent_test.go`（category=agent，7 个）：

| # | 场景 | 断言要点 | 维度覆盖 |
|---|---|---|---|
| 9 | react_evidence_success | 证据→succeeded + Verify passed | 正确性+效率 |
| 10 | plan_execute_steps | 产生 steps | 正确性+效率 |
| 11 | budget_cap_terminates | failed + error_code ∈ {tool_calls,model_calls}_budget_exceeded + budget.exceeded 事件 | 稳定性 |
| 12 | subagent_child_failure_contained | 子 run failed→父 run 仍受控终态 | 稳定性 |
| 13 | event_sequence_integrity | run.started < model.completed < run.finished，seq 单调 | 正确性+效率 |
| 14 | timeout_terminates | SetFallback 循环 + max_duration_seconds=1 → timed_out | 稳定性 |
| 15 | skill_disabled_denied | PUT settings/tools 关 skills → invoke_skill 被拒（tool_denied） | 正确性 |

实现注意：
- evals 包不能复用 tests/integration 的 `withTempWorkspace`（不同包）——在本包定义等价 helper（`builtin.SetDirs` + `t.Cleanup` 恢复 `./workspace` / `./knowledge`；包内测试串行，安全）。
- #6 memory / #8 tasks 需真实 session 行（runs.session_id FK），沿用 `h.CreateSession`。
- #12 子失败注入：子任务工具调用指向不存在的工具 → 子 run failed，父收到 failed 结果后继续收尾。
- #15 的 PUT settings/tools 请求体形状在计划阶段从 `server_tools_test.go` 现场确认（{skillsEnabled,...} 或后端 json tag 形状）。
- #14 timeout 场景有 ~1s 真实等待，可接受。

## 5. CI 与 Makefile

- **Makefile**：新增 `eval:` 目标 = `cd backend && go test ./tests/evals -count=1`；help 列表加一行。
- **ci.yml**：backend job 之后新增 `evals` job：checkout → setup-go@v5（go-version '1.27'，cache）→ `go test ./tests/evals -count=1` → `actions/upload-artifact@v4` 上传 `backend/eval-report.json`（`if: always()`）。eval 失败即 CI 失败（mock 确定性，可作门禁）。
- **文档**：backend/README.md + README.en.md 增「评估体系 / Evals」小节：跑法（make eval）、三维度含义、报告路径与产物。

## 6. 验收标准

1. `make eval` 通过且生成 `backend/eval-report.json`，含 summary/by_category/results 与三维度字段
2. 场景 ≥15：tool/agent 两类齐备；三维度各有场景体现（效率上限 ≥4 个场景，稳定性 ≥4 个场景）
3. ci.yml 语法有效（evals job 步骤完整）
4. 全量 `go test ./...` 与前端 `tsc --noEmit` + `vitest run` 不回归（产品代码零改动）
5. 文档同步（README zh/en）

## 7. 风险与对策

| 风险 | 对策 |
|---|---|
| TestMain 在报告写出前 t.Errorf 已判失败 | TestMain 在 m.Run() 返回后写报告（收集器已含全部结果）；失败细节以测试输出为准 |
| tasks/memory 场景 FIFO 脚本脆弱（父/子共享队列顺序） | 沿用批 3/批 4 已验证的排队模式（parent toolcall → child 输出 → parent text） |
| timeout 场景拖慢 suite | 上限 1s 预算 + 30s 兜底，实测预计 <3s |
| 事件计数口径（appendBudget 也算事件） | RequiredEvents 只断言存在性，不断言数量；效率维只用 h.Model.Calls() 与 tool.completed 计数 |
