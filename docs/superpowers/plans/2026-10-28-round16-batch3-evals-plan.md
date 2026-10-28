# Round-16 批3 evals 27→30 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增 3 个评估场景（幂等重放 / tasks 批量上限拒绝 / 审批二次决策 409），evals 27→30。

**Architecture:** 纯新增 `backend/tests/evals/scenarios_round16_test.go`，复用 R14 建立的模式：`r14ToolCall` 助手、`Scenario.Script` 返回幂等键（framework.go:84 `idem := s.Script(h)`）、`MidRun`/`PostCheck` 钩子。

**Tech Stack:** Go test + testutil.Harness + eino schema mock 模型。

**已验证事实（plan-time）：**
- POST /runs 返回 `{run_id, status, duplicated}`；重放同 `Idempotency-Key` → 201 + `duplicated:true` + 原 run_id（不重新执行，不消耗 mock 输出）。
- tasks 批量上限：`len(in.Tasks)>5` → handler 返回 `errRes` 字符串（工具"成功"，无 Go error）→ `tool.completed` payload 含 `batch size must be 1..5`，无子 run。
- 审批重复决策：POST 同一 approval → 409 `conflict`。
- `h.Tasks.ListChildrenByRun(ctx, runID)` 返回 `([]domain.Run, error)`。

---

### Task 1: scenarios_round16_test.go（3 场景）

**Files:**
- Create: `backend/tests/evals/scenarios_round16_test.go`

- [ ] **Step 1: 写场景（RED）**

场景 28 `run_idempotency_duplicate`（tool 类）：Script 返回固定键 `eval-r16-idem`；EnqueueText("done")；Want succeeded；PostCheck 用同 session + 同键重 POST，断言 201/duplicated:true/run_id 相同。

场景 29 `tasks_batch_limit_rejected`（tool 类）：EnqueueToolCall tasks 6 条；fallback：首轮无 Tool 角色重发 tasks、有 Tool 角色则收尾文本；Want succeeded + tool.completed；PostCheck payload 含 `batch size must be 1..5` 且 children 为空。

场景 30 `approval_double_decide_conflict`（tool 类）：file_edit 审批流（withTempWorkspace + old_text/new_text），MidRun approveFirst；PostCheck 里 `firstApprovalID` 再 POST `{approve:false}` → 409。

- [ ] **Step 2: 运行确认通过**

Run: `cd backend && go test ./tests/evals/ -run 'TestScenarioRunIdempotencyDuplicate|TestScenarioTasksBatchLimitRejected|TestScenarioApprovalDoubleDecideConflict' -count=1 -v`
Expected: 3 PASS（这些行为已存在于实现，属"评估覆盖新增"而非 bug 修复，无 RED 阶段；先写测试直接跑，失败则按输出修正场景脚本）

- [ ] **Step 3: 全量 evals + 回归**

Run: `cd backend && make test-eval && go test ./... -count=1`
Expected: 30 scenarios pass_rate 1；19 pkgs ok

- [ ] **Step 4: Commit**

```bash
git add backend/tests/evals/scenarios_round16_test.go
git commit -m "test(evals): three more scenarios — idempotency duplicate, tasks limit, approval conflict"
```

### Task 2: README 计数 27→30（zh/en）

- [ ] **Step 1:** backend/README.md + README.en.md 评估小节 27→30、分类计数（原 27 = 16 tool + 11 agent；新增 3 个均 tool 类 → 19 tool + 11 agent = 30）；grep 验证。
- [ ] **Step 2: Commit** `docs(evals): scenario count 27 -> 30 (zh/en)`

## 自检（self-review）

- 幂等场景 PostCheck 重 POST 不消耗 mock 输出（重放短路），EnqueueText 仅一次即可。
- tasks JSON 每条 `{"goal":"g%d"}`；上限检查先于子 run 创建，无需 file DB。
- 审批 409 场景 PostCheck 里 firstApprovalID 仍能取到（审批记录持久化）。
