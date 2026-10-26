# 批3 子 agent 并行（tasks 工具）Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:executing-plans to implement this plan（本仓库用户约束：不使用 subagent）。Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增 `tasks` 工具：一次委派多个只读子 agent 并行执行（默认并发 4、上限 8、批上限 5），结果按输入顺序返回；从现有 `task` handler 中抽出单子执行器复用。

**Architecture:** `internal/subagent` 包内重构：`NewTaskHandler` 闭包体抽成 `runOneChild(ctx, deps, tc, in) taskResult`；新 `TasksTool` 用 stdlib 信号量（buffered channel + WaitGroup，**不引入 golang.org/x/sync**——go.mod 无此依赖，spec 要求零新依赖）限并发。子运行行、预算 clamp（40%+命名上限）、只读白名单、深度守卫全部复用现有逻辑。runtime 侧 depth>=1 剔除名单从 `task` 扩展为 `task`+`tasks`。共享 wall-clock = executor 对该次工具调用的 120s 超时 ctx，父 cancel/deadline 经 ctx 级联到所有子。

**Tech Stack:** Go 1.27 stdlib（sync.WaitGroup + channel 信号量）、现有 store/sqlite（WAL 8 连接已支持并发写，busy_timeout 已按连接生效）、Eino ADK ChildExecutor。

**依据 spec:** docs/superpowers/specs/2026-10-26-agent-capabilities-design.md 批3 节（用户已批准：tasks 复数工具 / 并发默认 4 上限 8 / 批上限 5 / 结果按输入顺序 / 共享 120s / 同 clamp/depth/白名单；YAGNI：异步、写子、嵌套、池）。

---

### Task 1: runtime depth 剔除名单扩展（task+tasks）

**Files:**
- Modify: `backend/internal/runtime/runner_child.go`（filterToolNames + buildAgentFiltered 两处）
- Test: `backend/internal/runtime/runner_child_test.go`

- [ ] **Step 1.1: 扩展现有测试（先红）**

在 `runner_child_test.go` 的 `TestFilterToolNames` 中追加 `tasks` 断言（保持原有断言不动）：

```go
	got = filterToolNames([]string{"file_read", "tasks", "knowledge_search"}, 1)
	if containsStr(got, "tasks") {
		t.Fatalf("depth1 must strip tasks, got %v", got)
	}
	if !containsStr(got, "file_read") || !containsStr(got, "knowledge_search") {
		t.Fatalf("kept read-only tools expected, got %v", got)
	}
```

并更新函数上方注释 `depth>=1 剔除 "task"` → `depth>=1 剔除 "task"/"tasks"`。

- [ ] **Step 1.2: 跑测试确认红**

Run: `cd backend && go test ./internal/runtime/ -run TestFilterToolNames -count=1`
Expected: FAIL（`depth1 must strip tasks`）

- [ ] **Step 1.3: 实现 isAgenticTool 并替换两处硬编码**

`runner_child.go` 在 `filterToolNames` 上方加：

```go
// isAgenticTool：父子委托入口工具名；depth>=1 一律从子工具面剔除。
func isAgenticTool(name string) bool { return name == "task" || name == "tasks" }
```

`filterToolNames` 中 `if depth >= 1 && n == "task" {` → `if depth >= 1 && isAgenticTool(n) {`。
`buildAgentFiltered` 中 `if d.Name != "task" {` → `if !isAgenticTool(d.Name) {`（保留其上方注释，注释中 `task` 改为 `task/tasks`）。

- [ ] **Step 1.4: 跑测试确认绿**

Run: `cd backend && go test ./internal/runtime/ -count=1`
Expected: PASS（全部 runtime 测试）

- [ ] **Step 1.5: Commit**

```bash
git add backend/internal/runtime/runner_child.go backend/internal/runtime/runner_child_test.go
git commit -m "refactor(runtime): strip task/tasks from child tool surface via isAgenticTool"
```

---

### Task 2: 抽取单子执行器 runOneChild（纯重构，行为不变）

**Files:**
- Modify: `backend/internal/subagent/subagent.go`

**说明：** 无新测试；安全网 = 现有 `tests/integration/subagent_test.go` 两个用例 + 全量测试。重构后 `task` 工具输出 JSON 从 `{"status","error"}` 变为 taskResult 全形状（多 `run_id:""`、`text:""` 两个空键）——模型可见文本，非公开 API，无测试断言其形状，接受。

- [ ] **Step 2.1: 重构 subagent.go**

1) 删除 `errOut` 类型及其 `String` 方法，新增：

```go
// errRes：统一失败结果（等价旧 errOut 的 JSON 超集：多空 run_id/text 键）。
func errRes(msg string) taskResult { return taskResult{Status: "failed", Error: msg} }
```

2) 新增 deps 与 runOneChild（放在 `NewTaskHandler` 之后）：

```go
// childDeps：子委派所需的只读依赖集（reg/tasks/runner/cleaner）。
type childDeps struct {
	reg     *itool.Registry
	tasks   *store.TaskRepo
	runner  runtime.ChildExecutor
	cleaner runtime.CheckpointCleaner
}

// runOneChild：单个子目标的完整生命周期（白名单→clamp→建行→模型循环→分类→终态化）。
// ctx 必须是父活 ctx（cancel/deadline 级联）；DB 写全部走 bg（dead-ctx-safe）。
func runOneChild(ctx context.Context, deps childDeps, tc runtime.TaskContext, in taskIn) taskResult {
	toolNames, err := resolveReadOnlyTools(deps.reg, in.Tools)
	if err != nil {
		return errRes(err.Error())
	}
	budget := clampChildBudget(tc.ParentBudget, in)
	bg := context.Background()
	childID := uuid.NewString()
	if err := deps.tasks.CreateRun(bg, &domain.Run{
		ID: childID, OwnerID: tc.OwnerID, SessionID: tc.SessionID,
		Status: domain.RunQueued, Mode: "react", Goal: in.Goal,
		Budget: budget, ParentRunID: tc.ParentRunID, Attempt: 1,
	}); err != nil {
		return errRes("task: create child run: " + err.Error())
	}
	_ = deps.tasks.MarkStarted(bg, tc.OwnerID, childID)
	observability.IncrRunStarted()
	o := deps.runner.RunChild(ctx, runtime.ChildRequest{
		OwnerID: tc.OwnerID, ChildRunID: childID, Goal: in.Goal,
		ToolNames: toolNames, Budget: budget, Depth: tc.Depth + 1,
		CheckpointID: "ckpt_" + childID + "_" + fmt.Sprintf("%d", time.Now().UnixNano()),
	})
	switch {
	case o.PreDrainErr != nil:
		finalizeChild(bg, deps.tasks, tc.OwnerID, childID, domain.RunFailed, "runner_error", o.PreDrainErr.Error(), false, deps.cleaner)
		return taskResult{RunID: childID, Status: "failed", Error: "runner_error: " + o.PreDrainErr.Error()}
	case o.SawApproval:
		finalizeChild(bg, deps.tasks, tc.OwnerID, childID, domain.RunFailed, "child_needs_approval", "", false, deps.cleaner)
		return taskResult{RunID: childID, Status: "failed", Error: "child hit approval (unsupported for read-only child in v1)"}
	case o.Deadline:
		finalizeChild(bg, deps.tasks, tc.OwnerID, childID, domain.RunTimedOut, "timed_out", "child exceeded budget", false, deps.cleaner)
		return taskResult{RunID: childID, Status: "timed_out", Text: o.FinalText}
	case o.Cancelled:
		finalizeChild(bg, deps.tasks, tc.OwnerID, childID, domain.RunCancelled, "cancelled", "parent cancelled", false, deps.cleaner)
		return taskResult{RunID: childID, Status: "cancelled", Text: o.FinalText, Error: "cancelled"}
	case o.ModelFailed && o.ToolEvidence == 0:
		finalizeChild(bg, deps.tasks, tc.OwnerID, childID, domain.RunFailed, "model_failed", "child model failed, no tool evidence", false, deps.cleaner)
		return taskResult{RunID: childID, Status: "failed", Error: "model_failed"}
	case strings.TrimSpace(o.FinalText) == "" && o.ToolEvidence == 0:
		// 空结果守卫：无文本且无工具证据 → 失败，绝不判 succeeded。
		finalizeChild(bg, deps.tasks, tc.OwnerID, childID, domain.RunFailed, "empty_result", "child produced no result or evidence", false, deps.cleaner)
		return taskResult{RunID: childID, Status: "failed", Error: "empty_result"}
	default:
		rep := runtime.Verify(nil, o.FinalText, o.ToolEvidence) // 无 success_criteria → Passed 恒真
		rb, _ := json.Marshal(map[string]any{"passed": rep.Passed, "evidence": o.FinalText})
		_ = deps.tasks.SetResult(bg, childID, string(rb))
		if rep.Passed {
			finalizeChild(bg, deps.tasks, tc.OwnerID, childID, domain.RunSucceeded, "", "", true, deps.cleaner)
		} else {
			finalizeChild(bg, deps.tasks, tc.OwnerID, childID, domain.RunFailed, "verification_failed", "", false, deps.cleaner)
		}
		return taskResult{RunID: childID, Status: stringOfStatus(rep.Passed), Text: o.FinalText}
	}
}
```

3) `NewTaskHandler` 闭包体替换为：

```go
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		tc, ok := runtime.TaskContextFrom(ctx)
		if !ok || tc.Depth >= 1 {
			return errRes("task not available at this depth").String(), nil
		}
		var in taskIn
		if err := json.Unmarshal(args, &in); err != nil || strings.TrimSpace(in.Goal) == "" {
			return errRes("task: invalid goal").String(), nil
		}
		deps := childDeps{reg: reg, tasks: tasks, runner: runner, cleaner: cleaner}
		return runOneChild(ctx, deps, tc, in).String(), nil
	}
```

- [ ] **Step 2.2: 验证重构零回归**

Run: `cd backend && go build ./... && go vet ./... && go test ./internal/subagent/... ./tests/integration/ -run 'TestTask|TestSubagent' -count=1 -v 2>&1 | tail -20`
Expected: build/vet OK；`TestTaskToolDelegatesReadOnlyChild`、`TestTaskToolReadOnlyInvariant` PASS。（若无匹配测试名，跑全量 `go test ./tests/integration/ -count=1`。）

- [ ] **Step 2.3: Commit**

```bash
git add backend/internal/subagent/subagent.go
git commit -m "refactor(subagent): extract runOneChild from task handler"
```

---

### Task 3: TasksTool 批量并行工具（TDD）

**Files:**
- Create: `backend/internal/subagent/batch_test.go`
- Modify: `backend/internal/subagent/subagent.go`

- [ ] **Step 3.1: 写失败测试 batch_test.go**

```go
package subagent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/runtime"
	"rivus-agent-backend/internal/store"
)

// fakeRunner：记录 RunChild 调用；可选注入并发探测。
type fakeRunner struct {
	mu    sync.Mutex
	calls []runtime.ChildRequest
	probe func() // 并发探测钩子（可 nil）
}

func (f *fakeRunner) RunChild(ctx context.Context, req runtime.ChildRequest) runtime.ChildOutcome {
	f.mu.Lock()
	f.calls = append(f.calls, req)
	f.mu.Unlock()
	if f.probe != nil {
		f.probe()
	}
	return runtime.ChildOutcome{FinalText: "done:" + req.Goal, ToolEvidence: 1}
}

type fakeCleaner struct{ deleted []string }

func (c *fakeCleaner) DeleteCheckpointsByRun(_ context.Context, runID string) {
	c.deleted = append(c.deleted, runID)
}

// newBatchEnv：内存库 + 真实 TaskRepo（runOneChild 的 DB 路径全真）。
func newBatchEnv(t *testing.T) (childDeps, *store.TaskRepo) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "batch.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	tasks := store.NewTaskRepo(db)
	return childDeps{tasks: tasks}, tasks
}

func batchTC() runtime.TaskContext {
	return runtime.TaskContext{OwnerID: "o1", ParentRunID: "p1", SessionID: "s1",
		ParentBudget: domain.Budget{MaxModelCalls: 10, MaxToolCalls: 10, MaxIterations: 6,
			MaxDurationSeconds: 120, MaxOutputBytes: 50000}, Depth: 0}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestTasksHandlerResultsInInputOrder(t *testing.T) {
	deps, tasks := newBatchEnv(t)
	runner := &fakeRunner{}
	deps.runner = runner
	deps.reg = newEmptyRegistry(t)
	h := NewTasksHandler(deps.reg, deps.tasks, runner, &fakeCleaner{})
	in := mustJSON(t, batchIn{Tasks: []taskIn{{Goal: "g1"}, {Goal: "g2"}, {Goal: "g3"}}})
	out, err := h(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	var results []taskResult
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		t.Fatalf("output not array: %v: %s", err, out)
	}
	if len(results) != 3 {
		t.Fatalf("want 3 results, got %d", len(results))
	}
	for i, want := range []string{"g1", "g2", "g3"} {
		if results[i].Text != "done:"+want {
			t.Fatalf("results[%d].Text=%q want done:%s (order must match input)", i, results[i].Text, want)
		}
		if results[i].Status != "succeeded" {
			t.Fatalf("results[%d].Status=%q want succeeded", i, results[i].Status)
		}
	}
	if n := len(runner.snapshot(t)); n != 3 {
		t.Fatalf("want 3 child runs, got %d", n)
	}
	// 子行已终态化
	for _, c := range mustChildren(t, tasks, "p1") {
		if c.Status != domain.RunSucceeded {
			t.Fatalf("child %s status=%s want succeeded", c.ID, c.Status)
		}
	}
}

func TestTasksHandlerConcurrencyBound(t *testing.T) {
	deps, _ := newBatchEnv(t)
	var inFlight, maxInFlight int64
	runner := &fakeRunner{probe: func() {
		cur := atomic.AddInt64(&inFlight, 1)
		for {
			old := atomic.LoadInt64(&maxInFlight)
			if cur <= old || atomic.CompareAndSwapInt64(&maxInFlight, old, cur) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		atomic.AddInt64(&inFlight, -1)
	}}
	deps.runner = runner
	deps.reg = newEmptyRegistry(t)
	h := NewTasksHandler(deps.reg, deps.tasks, runner, &fakeCleaner{})
	in := mustJSON(t, batchIn{Tasks: []taskIn{{Goal: "a"}, {Goal: "b"}, {Goal: "c"}, {Goal: "d"}}, MaxConcurrency: 2})
	if _, err := h(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	got := atomic.LoadInt64(&maxInFlight)
	if got != 2 {
		t.Fatalf("max in-flight=%d want exactly 2 (default would be 4; serial would be 1)", got)
	}
}

func TestTasksHandlerValidation(t *testing.T) {
	deps, tasks := newBatchEnv(t)
	runner := &fakeRunner{}
	deps.runner = runner
	deps.reg = newEmptyRegistry(t)
	h := NewTasksHandler(deps.reg, deps.tasks, runner, &fakeCleaner{})

	cases := []struct {
		name string
		tc   runtime.TaskContext
		in   batchIn
	}{
		{"no context", runtime.TaskContext{}, batchIn{Tasks: []taskIn{{Goal: "x"}}}},
		{"child depth", runtime.TaskContext{Depth: 1}, batchIn{Tasks: []taskIn{{Goal: "x"}}}},
		{"empty batch", batchTC(), batchIn{}},
		{"too many", batchTC(), batchIn{Tasks: []taskIn{{Goal: "1"}, {Goal: "2"}, {Goal: "3"}, {Goal: "4"}, {Goal: "5"}, {Goal: "6"}}}},
		{"empty goal", batchTC(), batchIn{Tasks: []taskIn{{Goal: "ok"}, {Goal: "  "}}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			if c.tc.OwnerID != "" {
				ctx = runtime.WithTaskContext(ctx, c.tc)
			}
			out, err := h(ctx, mustJSON(t, c.in))
			if err != nil {
				t.Fatal(err)
			}
			var res taskResult
			_ = json.Unmarshal([]byte(out), &res)
			if res.Status != "failed" || res.Error == "" {
				t.Fatalf("want failed with error, got %s", out)
			}
		})
	}
	// 全部失败路径不得建子行
	if kids, err := tasks.ListChildrenByRun(context.Background(), "p1"); err != nil || len(kids) != 0 {
		t.Fatalf("validation failures must not create children: %v %v", kids, err)
	}
}

func newEmptyRegistry(t *testing.T) *registryAlias {
	t.Helper()
	return newRegistry(t)
}

func mustChildren(t *testing.T, tasks *store.TaskRepo, parent string) []domain.Run {
	t.Helper()
	kids, err := tasks.ListChildrenByRun(context.Background(), parent)
	if err != nil {
		t.Fatal(err)
	}
	return kids
}
```

**注意（写测试时落实）：** 上面 `newEmptyRegistry`/`registryAlias` 是占位——实现时直接用 `itool.NewRegistry()`（返回 `*itool.Registry`），把 helper 简化为：

```go
func newEmptyRegistry(t *testing.T) *itool.Registry {
	t.Helper()
	return itool.NewRegistry()
}
```

（import 增加 `itool "rivus-agent-backend/internal/tool"`；删除 `newRegistry`/`registryAlias` 残留。）`fakeRunner.snapshot` 同理落实为：

```go
func (f *fakeRunner) snapshot(t *testing.T) []runtime.ChildRequest {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]runtime.ChildRequest(nil), f.calls...)
}
```

（并把 `TestTasksHandlerResultsInInputOrder` 中 `len(runner.snapshot(t))` 保持。）

- [ ] **Step 3.2: 跑测试确认红**

Run: `cd backend && go test ./internal/subagent/ -count=1`
Expected: 编译 FAIL（`NewTasksHandler` undefined）

- [ ] **Step 3.3: 实现 TasksTool（subagent.go 末尾追加）**

```go
const tasksToolName = "tasks"

const (
	batchMaxChildren   = 5
	batchDefaultConcur = 4
	batchMaxConcur     = 8
)

// TasksTool 返回 tasks 工具：批量并行委派只读子 agent。
// 并发上限默认 4、请求上限 8；批大小 1..5；结果按输入顺序返回；
// 共享 wall-clock = executor 对本次调用的 120s 超时（父 cancel/deadline 经 ctx 级联）。
func TasksTool(reg *itool.Registry, tasks *store.TaskRepo, runner runtime.ChildExecutor, cleaner runtime.CheckpointCleaner) itool.Definition {
	h := NewTasksHandler(reg, tasks, runner, cleaner)
	return itool.Definition{
		Name: tasksToolName,
		Description: "Delegate multiple read-only sub-goals to child agents in parallel; runs synchronously and returns results in input order. Args: {\"tasks\":[{\"goal\":\"...\",\"tools\":[...],\"max_model_calls\":N}],\"max_concurrency\":4}",
		Version:     "v1",
		InputSchema: `{"type":"object","properties":{"tasks":{"type":"array","maxItems":5,"items":{"type":"object","properties":{"goal":{"type":"string"},"tools":{"type":"array","items":{"type":"string"}},"max_model_calls":{"type":"integer"},"max_tool_calls":{"type":"integer"},"max_iterations":{"type":"integer"}},"required":["goal"]}},"max_concurrency":{"type":"integer"}},"required":["tasks"]}`,
		Risk:        itool.RiskRead, Approval: itool.ApprovalNever, Idempotent: false, TimeoutMs: 120000, MaxResultBytes: 64 * 1024,
		Handler: h,
	}
}

type batchIn struct {
	Tasks          []taskIn `json:"tasks"`
	MaxConcurrency int      `json:"max_concurrency"`
}

// NewTasksHandler：tasks 工具入口。并发用 stdlib 信号量（buffered channel + WaitGroup），
// 不引入 golang.org/x/sync（零新依赖约束）。
func NewTasksHandler(reg *itool.Registry, tasks *store.TaskRepo, runner runtime.ChildExecutor, cleaner runtime.CheckpointCleaner) itool.Handler {
	deps := childDeps{reg: reg, tasks: tasks, runner: runner, cleaner: cleaner}
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		tc, ok := runtime.TaskContextFrom(ctx)
		if !ok || tc.Depth >= 1 {
			return errRes("tasks not available at this depth").String(), nil
		}
		var in batchIn
		if err := json.Unmarshal(args, &in); err != nil {
			return errRes("tasks: invalid input").String(), nil
		}
		if len(in.Tasks) == 0 || len(in.Tasks) > batchMaxChildren {
			return errRes(fmt.Sprintf("tasks: batch size must be 1..%d, got %d", batchMaxChildren, len(in.Tasks))).String(), nil
		}
		for i := range in.Tasks {
			if strings.TrimSpace(in.Tasks[i].Goal) == "" {
				return errRes(fmt.Sprintf("tasks[%d]: empty goal", i)).String(), nil
			}
		}
		limit := in.MaxConcurrency
		if limit <= 0 {
			limit = batchDefaultConcur
		}
		if limit > batchMaxConcur {
			limit = batchMaxConcur
		}
		results := make([]taskResult, len(in.Tasks))
		sem := make(chan struct{}, limit)
		var wg sync.WaitGroup
		for i := range in.Tasks {
			wg.Add(1)
			sem <- struct{}{} // 背压：超过 limit 的迭代阻塞在主 goroutine
			go func(i int) {
				defer wg.Done()
				defer func() { <-sem }()
				results[i] = runOneChild(ctx, deps, tc, in.Tasks[i])
			}(i)
		}
		wg.Wait()
		b, _ := json.Marshal(results)
		return string(b), nil
	}
}
```

subagent.go import 增加 `"sync"`。

- [ ] **Step 3.4: 跑测试确认绿**

Run: `cd backend && go test ./internal/subagent/ -count=1 -v 2>&1 | tail -20`
Expected: 全部 PASS（含新增 4 个测试 + 旧测试无）
注意：若 `store.Open(t.TempDir())` 文件库建表失败或 runs 表非空约束缺失，按 schema 实情修正 batchTC 的字段填充（这正是真库单测的价值）。

- [ ] **Step 3.5: Commit**

```bash
git add backend/internal/subagent/subagent.go backend/internal/subagent/batch_test.go
git commit -m "feat(subagent): tasks tool runs read-only children in parallel with bounded concurrency"
```

---

### Task 4: app.go 注册 + 集成测试（端到端）

**Files:**
- Modify: `backend/internal/app/app.go:79` 附近
- Create: `backend/tests/integration/batch_test.go`

- [ ] **Step 4.1: 写失败集成测试**

```go
package integration

import (
	"context"
	"testing"
	"time"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/tests/testutil"
)

// 并发=1 使两个子按输入顺序串行消费共享 ScriptedModel FIFO → 断言确定性。
func TestTasksToolTwoChildrenSerial(t *testing.T) {
	h := testutil.New(t)
	h.Model.EnqueueToolCall("tc", "tasks", `{"tasks":[{"goal":"g1","tools":["file_read"]},{"goal":"g2","tools":["file_read"]}],"max_concurrency":1}`)
	h.Model.EnqueueText("child one final")  // 子1
	h.Model.EnqueueText("child two final")  // 子2
	h.Model.EnqueueText("parent final done") // 父
	ses := h.CreateSession("s")
	id, code, _ := h.CreateRun(ses, fixturePayload(t, ses), "batch-1")
	if code != 201 {
		t.Fatalf("create run: %d", code)
	}
	run := h.WaitTerminal(id, 30*time.Second)
	if run.Status != domain.RunSucceeded {
		t.Fatalf("parent want succeeded got %s (%s)", run.Status, run.ErrorSummary)
	}
	kids, err := h.Tasks.ListChildrenByRun(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(kids) != 2 {
		t.Fatalf("want 2 children, got %d", len(kids))
	}
	saw := map[string]bool{}
	for _, c := range kids {
		if !c.Status.Terminal() {
			t.Fatalf("child %s not terminal: %s", c.ID, c.Status)
		}
		saw[c.Goal] = true
		evs, _ := h.Events.ListAfter(context.Background(), c.ID, 0, 500)
		var start, finish bool
		for _, e := range evs {
			if e.Type == domain.EvtRunStarted {
				start = true
			}
			if e.Type == domain.EvtRunFinished {
				finish = true
			}
		}
		if !start || !finish {
			t.Fatalf("child %s events missing: start=%v finish=%v", c.ID, start, finish)
		}
	}
	if !saw["g1"] || !saw["g2"] {
		t.Fatalf("children goals wrong: %v", saw)
	}
}
```

- [ ] **Step 4.2: 跑测试确认红**

Run: `cd backend && go test ./tests/integration/ -run TestTasksToolTwoChildrenSerial -count=1`
Expected: FAIL（父 failed：`unknown tool "tasks"` → tool_not_found）

- [ ] **Step 4.3: app.go 注册**

app.go L79 的 `registry.Register(subagent.TaskTool(...))` 之后追加：

```go
	if err := registry.Register(subagent.TasksTool(registry, tasks, runner, runner)); err != nil {
		return nil, err
	}
```

- [ ] **Step 4.4: 跑测试确认绿 + 子事件 FIFO 顺序**

Run: `cd backend && go test ./tests/integration/ -run 'TestTasksTool|TestTaskTool' -count=1 -v 2>&1 | tail -10`
Expected: 全 PASS。

- [ ] **Step 4.5: Commit**

```bash
git add backend/internal/app/app.go backend/tests/integration/batch_test.go
git commit -m "feat(app): register tasks tool; integration test for parallel child delegation"
```

---

### Task 5: 文档同步 + 全量验收

**Files:**
- Modify: `backend/README.md`（工具清单：+tasks）
- Modify: `backend/README.en.md`（同上英文）
- Modify: `README.md` / `README.en.md`（若工具数出现在根 README 表格，一并 +1）

- [ ] **Step 5.1: 更新工具清单**

`grep -n "invoke_skill\|工具" backend/README.md README.md | head` 定位清单行；在工具表 `task` 行后加 `tasks`（描述：批量并行委派只读子 agent，默认并发 4 / 上限 8 / 批上限 5，结果按输入顺序）。en 版同步翻译。仅当清单存在时改——没有就不动（不造新章节）。

- [ ] **Step 5.2: 后端全量验收**

Run: `cd backend && go build ./... && go vet ./... && go test ./... -count=1 -timeout 600s 2>&1 | tail -20`
Expected: build/vet 干净；所有包 ok。

- [ ] **Step 5.3: 前端回归（零改动预期）**

Run: `cd frontend && npx tsc --noEmit && npx vitest run 2>&1 | tail -6`
Expected: tsc 0 错误；vitest 全过（127+）。

- [ ] **Step 5.4: Commit + 汇报**

```bash
git add -A backend/README.md backend/README.en.md README.md README.en.md
git commit -m "docs(readme): document tasks parallel delegation tool"
```

完成后按 spec 验收项向用户汇报（并行结果顺序、并发上限、批上限、深度守卫、零新依赖）。

---

## Self-Review（已按 skill 自审，无 subagent）

| 检查项 | 结论 |
|---|---|
| 零新依赖 | ✅ 信号量 = buffered channel + WaitGroup；go.mod 无 golang.org/x/sync（已验证） |
| 复用不重写 | ✅ clamp/resolveReadOnlyTools/RunChild/finalizeChild 原样复用；runOneChild 是搬运非重写 |
| 结果顺序 | ✅ results[i] 定长切片按下标写，并发安全（各 goroutine 独写下标） |
| 并发上限可测 | ✅ max-in-flight 探针断言 ==2（区分默认4与串行1） |
| 共享 wall-clock | ✅ 单次工具调用 = 单一 executor 120s ctx，全部子共享，父 cancel 级联（RunChild 复用父 ctx） |
| FIFO 确定性 | ✅ 集成测试 max_concurrency=1 强制串行消费共享 FIFO |
| task 输出形状变化 | ⚠️ 已知接受：errOut→taskResult 后失败 JSON 多空 run_id/text 键（模型可见文本，非公开 API，无测试断言） |
| :memory: 陷阱 | ✅ 批3 单测用 t.TempDir() 文件库（避 per-conn :memory: 分片） |
| 深度守卫双保险 | ✅ handler tc.Depth>=1 拒 + runner depth 剔除工具面（isAgenticTool 覆盖两处） |
| YAGNI | ✅ 无异步、无写子、无嵌套、无池 |
