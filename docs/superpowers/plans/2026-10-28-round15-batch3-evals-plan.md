# Round-15 批3：evals 24→27 实施计划

> **For agentic workers:** REQUIRED: Use superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增 3 个评估场景：run 详情三端点、运行状态过滤（端到端验证批2）、审批拒绝路径。

**Architecture:** 全部复用既有框架钩子（Script/MidRun/PostCheck/NewHarness），不改动 framework.go；场景文件 scenarios_round15_test.go。

**Tech Stack:** Go test / testutil harness（mock 模型 FIFO）/ h.Do HTTP 断言。

**Spec:** docs/superpowers/specs/2026-10-28-round15-i18n-filter-evals-design.md §3.3

---

## Chunk 1

### Task 1: scenarios_round15_test.go（3 场景）

**Files:**
- Create: `backend/tests/evals/scenarios_round15_test.go`

- [ ] **Step 1: 写 3 场景**（复用 r14ToolCall / approveFirst / firstApprovalID / withTempWorkspace 既有 helper）：
  1. `run_detail_api`（tool）：EnqueueText 成功 run → PostCheck 三连：`GET /runs/{id}` 200 + body 含 `"id":"<runID>"`；`GET /runs/{id}/events?after=0` 200 + 含 `run.started`；`GET /runs/{id}/approvals` 200 + 含 `"approvals"`。
  2. `runs_status_filter`（agent）：主 run `max_output_bytes:10` 超限失败（R14 场景 23 手法）→ PostCheck 内造第二个成功 run（`h.Model.EnqueueText("fine")` → `h.CreateRun(run.SessionID, {goal}, idem)` → WaitTerminal）→ `GET /api/v1/runs?status=failed` 仅回败者 / `?status=succeeded` 仅回成者 / 无参两者都有。
  3. `approval_rejected_flow`（tool）：withTempWorkspace + file_edit（RiskWrite 条件审批）→ MidRun `firstApprovalID` + POST `{"approve":false}` → Want failed + `approval_rejected`；PostCheck：无 tool.completed（工具未执行）+ approval.decided 载荷含 `"approved":false`。
- [ ] **Step 2**: `go test ./tests/evals/ -run 'TestScenarioRunDetailAPI|TestScenarioRunsStatusFilter|TestScenarioApprovalRejected' -count=1` → 全 PASS（实现即测试；若先写测试文件不写场景则无编译目标，本任务为纯增量测试，RED 阶段 = 首跑失败修正）。
- [ ] **Step 3**: `make test-eval` → 27 场景 pass_rate 1。
- [ ] **Step 4**: `go test ./... -count=1` 回归全绿。
- [ ] **Step 5: Commit** `test(evals): three more scenarios — run detail api, status filter, approval rejected`

## Self-review 备注

- 拒绝路径**无需 SetFallback**：拒绝即终态（RunFailed + approval_rejected），没有审批后二次执行；与批准路径（需重发工具调用）本质不同。
- runs_status_filter 第二个 run 的 EnqueueText 必须在 CreateRun **之前**（FIFO 消费时序）。
- owner-wide 过滤断言基于 id 集合比较（JSON 解码 []domain.Run）。
