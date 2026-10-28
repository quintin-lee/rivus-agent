# Round 14 批2 实现计划：evals 20→24 + bench 3→8

> **For agentic workers:** REQUIRED: Use superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 4 个新评估场景（skill references / 编辑回读 / output_bytes 预算 / 中途取消）+ 5 个新基准（幂等命中 / 预算门禁 / 事件读 / run 列表读 / 混合读写）。

**Architecture:** 场景复用既有 evals 框架钩子（Script/MidRun/PostCheck/NewHarness）与 helpers（withTempWorkspace/writeFileRel/findEvent/loopToolSlow/reissueToolThenText/approveFirst/allowHTTPFetch）；bench 复用 testutil.New(b) 与 store.Open 文件 DB（8 连接，R2）。

**Tech Stack:** Go testing；无新依赖。

**Spec:** docs/superpowers/specs/2026-10-28-round14-observe-evals-frontend-design.md §3.2

---

## 已验证事实

- harness.go:221-230（NewToolConfigHarness）：demo skill 位于 `t.TempDir()` 下的 `demo/SKILL.md`（内容含 `DEMO SKILL BODY`），**无 references/ 目录** → 本计划在 harness 中补写 `demo/references/cheatsheet.md`（对既有 invoke_skill 测试无影响：它们只断言 tool.completed 存在或 body 包含）。
- helpers（tests/evals/helpers_test.go）：withTempWorkspace(t)（返回 root）、writeFileRel(root, rel, content)、findEvent(t, h, runID, typ)（两值）、loopToolSlow(id, name, args, d)、reissueToolThenText(id, name, args, finalText)；approveFirst 在 scenarios_more_test.go（同包可复用）。
- 预算错误码 `output_bytes_budget_exceeded`（runtime/budget.go:74）；OverBudget 在事件循环 post-switch 触发（R1）→ 单轮 EnqueueText 即可触发（model.completed → AddOutput → 超限 → Cancel → 终态 budget 分支）。
- 取消场景无 MidRun 钩子（仅 waiting_approval 触发）→ 在 EnqueueFunc 内部用 R9 的 `GET /api/v1/runs?session_id=` 找 marker run 再 POST cancel（闭包在 CreateRun 前无法拿到 runID，用 goal marker 定位）。
- tool.completed 事件 payload 携带 args/output（R5 载荷增强，1KB 截断）→ 可断言工具输出内容。
- bench 现状：bench_test.go 3 个 Benchmark（EventAppend :memory: / ToolExecute / APICreateRun）+ 1 个 TestLoad；bench 包已 import store/domain/tool/builtin/testutil。
- store.Open(文件路径) = 8 连接（R2）；FK 生效 → 先 CreateSession 再 CreateRun。

---

### Task 1: harness demo skill 增加 references + 4 个新场景

**Files:**
- Modify: `backend/tests/testutil/harness.go:226-228`（SKILL.md 写入后补 references/cheatsheet.md）
- Create: `backend/tests/evals/scenarios_round14_test.go`

- [ ] **Step 1: harness 补 references 文件**

harness.go 中 `WriteFile(..., "SKILL.md", ...)` 成功后追加：

```go
		if err := os.MkdirAll(filepath.Join(skillDir, "demo", "references"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skillDir, "demo", "references", "cheatsheet.md"),
			[]byte("# cheatsheet\nR14-CHEATSHEET-MARKER\n"), 0o644); err != nil {
			t.Fatal(err)
		}
```

- [ ] **Step 2: 写 4 个场景（新文件 scenarios_round14_test.go）**

```go
package evals

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/tests/testutil"
)

// 场景 21：invoke_skill 加载 references/ 子文件。
func TestScenarioInvokeSkillReferenceLoad(t *testing.T) {
	Run(t, Scenario{
		Name: "invoke_skill_reference_load", Category: "tool",
		Goal: "load the cheatsheet reference of the demo skill",
		Script: func(h *testutil.Harness) {
			h.Model.EnqueueToolCall("tc1", "invoke_skill", `{"name":"demo","reference":"cheatsheet.md"}`)
			h.Model.EnqueueText("loaded")
		},
		Want: Expect{TerminalStatus: domain.RunSucceeded, MaxToolCalls: 1,
			RequiredEvents: []domain.EventType{domain.EvtToolCompleted}},
		NewHarness: testutil.NewToolConfigHarness,
		PostCheck: func(t *testing.T, h *testutil.Harness, runID string, run domain.Run) {
			ev, ok := findEvent(t, h, runID, domain.EvtToolCompleted)
			if !ok {
				t.Fatal("no tool.completed")
			}
			if !strings.Contains(ev.PayloadJSON, "R14-CHEATSHEET-MARKER") {
				t.Fatalf("reference content not in payload: %s", ev.PayloadJSON)
			}
		},
	})
}

// 场景 22：file_edit（审批）后 file_read 回读，内容落盘一致。
func TestScenarioFileEditReadRoundtrip(t *testing.T) {
	root := withTempWorkspace(t)
	if err := writeFileRel(root, "a.txt", "hello\n"); err != nil {
		t.Fatal(err)
	}
	Run(t, Scenario{
		Name: "file_edit_read_roundtrip", Category: "tool",
		Goal: "edit a file with approval then read it back",
		Script: func(h *testutil.Harness) {
			h.Model.EnqueueToolCall("tc1", "file_edit", `{"path":"a.txt","old":"hello","new":"world"}`)
			h.Model.SetFallback(reissueToolThenText("tc2", "file_read", `{"path":"a.txt"}`, "edited"))
		},
		MidRun: func(t *testing.T, h *testutil.Harness, runID string) {
			approveFirst(t, h, runID)
		},
		Want: Expect{TerminalStatus: domain.RunSucceeded},
		PostCheck: func(t *testing.T, h *testutil.Harness, runID string, run domain.Run) {
			b, err := os.ReadFile(filepath.Join(root, "a.txt"))
			if err != nil || string(b) != "world\n" {
				t.Fatalf("file content = %q, err = %v", b, err)
			}
			if ev, ok := findEvent(t, h, runID, domain.EvtToolCompleted); ok &&
				!strings.Contains(ev.PayloadJSON, "world") {
				t.Fatalf("file_read output missing content: %s", ev.PayloadJSON)
			}
		},
	})
}

// 场景 23：output_bytes 预算维度终止运行（三个预算维度中最后补齐的一个）。
func TestScenarioBudgetOutputBytesCap(t *testing.T) {
	Run(t, Scenario{
		Name: "budget_output_bytes_cap", Category: "agent",
		Goal: "emit more output bytes than the budget allows",
		Budget: domain.Budget{MaxDurationSeconds: 30, MaxModelCalls: 10, MaxToolCalls: 10,
			MaxIterations: 10, MaxOutputBytes: 10},
		Script: func(h *testutil.Harness) {
			h.Model.EnqueueText("this single output is definitely longer than ten bytes")
		},
		Want: Expect{TerminalStatus: domain.RunFailed, ErrorCode: "output_bytes_budget_exceeded",
			RequiredEvents: []domain.EventType{domain.EvtBudgetExceeded}},
	})
}

// 场景 24：运行中取消（POST /cancel），终态 cancelled。
func TestScenarioCancelRunningRun(t *testing.T) {
	Run(t, Scenario{
		Name: "cancel_running_run", Category: "agent",
		Goal: "eval-cancel-marker",
		Budget: domain.Budget{MaxDurationSeconds: 30, MaxModelCalls: 10, MaxToolCalls: 10,
			MaxIterations: 10, MaxOutputBytes: 50000},
		Script: func(h *testutil.Harness) {
			h.Model.EnqueueToolCall("tc1", "loop_tool_slow", `{}`)
			h.Model.SetFallback(func(i int, _ []*schema.Message) (*schema.Message, error) {
				// 第二轮模型调用时取消：owner-wide 列表（R9）按 goal marker 定位 run。
				code, body, err := h.Do(http.MethodGet, "/api/v1/runs?limit=50", nil, nil)
				if err != nil || code != 200 {
					return nil, fmt.Errorf("list runs: code=%d err=%v", code, err)
				}
				var listed struct {
					Runs []domain.Run `json:"runs"`
				}
				if err := json.Unmarshal(body, &listed); err != nil {
					return nil, err
				}
				for _, r := range listed.Runs {
					if r.Goal == "eval-cancel-marker" {
						if c, _, _ := h.Do(http.MethodPost, "/api/v1/runs/"+r.ID+"/cancel",
							map[string]string{}, nil); c != 200 {
							return nil, fmt.Errorf("cancel: %d", c)
						}
						break
					}
				}
				return schemaText("cancelling"), nil
			})
		},
		Want: Expect{TerminalStatus: domain.RunCancelled,
			RequiredEvents: []domain.EventType{domain.EvtRunCancelled}},
		PostCheck: func(t *testing.T, h *testutil.Harness, runID string, run domain.Run) {
			if run.ErrorCode != "" {
				t.Fatalf("cancelled run should have no error code, got %q", run.ErrorCode)
			}
		},
	})
}
```

执行注意：
- `h.Do` 返回值签名按 harness 实际为准（此前用法 `code, body := h.Do(...)` 三值/两值在测试中出现过差异 —— 写之前先 grep harness.go 的 Do 定义，按实际调整）。
- `schemaText` 助手若不存在，用 helpers_test.go 中既有文本消息构造方式；EnqueueFunc 的返回类型 `*schema.Message`（import `github.com/cloudwego/eino/schema`）。
- `domain.RunCancelled` / `domain.EvtRunCancelled` 常量名以 domain 包实际为准（grep 确认）。
- loop_tool_slow 为 evals 包内已注册的慢工具（helpers_test.go loopToolSlow 配套的 Definition；若注册名不同按实际调整，见 scenarios_agent_test.go timeout 场景用法）。

- [ ] **Step 3: 运行确认通过（新场景直接红→修→绿）**

Run: `cd backend && go test ./tests/evals/ -run 'TestScenarioInvokeSkillReferenceLoad|TestScenarioFileEditReadRoundtrip|TestScenarioBudgetOutputBytesCap|TestScenarioCancelRunningRun' -count=1 -v`
Expected: 4 个 PASS（编译错误/API 签名差异按上表执行注意修正）。

- [ ] **Step 4: 全量 evals 回归**

Run: `cd backend && make test-eval`
Expected: 24 scenarios，pass_rate 1。

- [ ] **Step 5: Commit**

```bash
git add backend/tests/testutil/harness.go backend/tests/evals/scenarios_round14_test.go
git commit -m "test(evals): four more scenarios — skill references, edit roundtrip, output-bytes cap, mid-run cancel"
```

---

### Task 2: bench 3→8

**Files:**
- Modify: `backend/tests/bench/bench_test.go`（追加 5 个 Benchmark）

- [ ] **Step 1: 追加基准**

```go
// 幂等缓存命中开销（同 (runID, toolCallID) 重复执行）。
func BenchmarkExecutorIdempotentHit(b *testing.B) {
	reg := tool.NewRegistry()
	def := tool.Definition{
		Name: "echo_bench", Description: "echo", Risk: tool.RiskRead,
		Approval: tool.ApprovalNever, Idempotent: true,
		TimeoutMs: 1000, MaxResultBytes: 1024,
		Handler: func(_ context.Context, _ json.RawMessage) (string, error) { return "ok", nil },
	}
	if err := reg.Register(def); err != nil {
		b.Fatal(err)
	}
	ex := tool.NewExecutor(reg)
	req := tool.ExecRequest{RunID: "r", ToolName: "echo_bench", Args: json.RawMessage(`{}`)}
	ctx := context.Background()
	if _, err := ex.Execute(ctx, req); err != nil { // 预热缓存
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ex.Execute(ctx, req); err != nil {
			b.Fatal(err)
		}
	}
}

// 预算门禁检查开销（未超限路径，含互斥锁）。
func BenchmarkBudgetOverBudget(b *testing.B) {
	tr := runtime.NewBudgetTracker(domain.Budget{MaxModelCalls: 1000, MaxToolCalls: 1000,
		MaxIterations: 1000, MaxOutputBytes: 1 << 30, MaxDurationSeconds: 3600})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tr.AddModelCall()
		tr.AddOutput(64)
		if _, hit := tr.OverBudget(); hit {
			b.Fatal("unexpected budget trip")
		}
	}
}

// 事件读：预写 100 条后 ListAfter 全量读。
func BenchmarkEventsListAfter(b *testing.B) {
	db, err := store.Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	tasks := store.NewTaskRepo(db)
	ev := store.NewEventRepo(db)
	ctx := context.Background()
	ses, err := tasks.CreateSession(ctx, "u1", "bench-read")
	if err != nil {
		b.Fatal(err)
	}
	run := &domain.Run{ID: "run_read", SessionID: ses, OwnerID: "u1", Status: domain.RunQueued,
		Mode: "react", Goal: "read",
		Budget:  domain.Budget{MaxDurationSeconds: 60, MaxModelCalls: 5, MaxToolCalls: 5, MaxIterations: 3, MaxOutputBytes: 1000},
		Attempt: 1}
	if err := tasks.CreateRun(ctx, run); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if _, err := ev.Append(ctx, "run_read", domain.EvtToolCompleted, `{"n":1}`, "normal"); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ev.ListAfter(ctx, "run_read", 0, 100); err != nil {
			b.Fatal(err)
		}
	}
}

// 顶层 run 列表读（owner 视角，R9）。
func BenchmarkListRunsByOwner(b *testing.B) {
	db, err := store.Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	tasks := store.NewTaskRepo(db)
	ctx := context.Background()
	ses, err := tasks.CreateSession(ctx, "u1", "bench-list")
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		r := &domain.Run{ID: fmt.Sprintf("run_list_%02d", i), SessionID: ses, OwnerID: "u1",
			Status: domain.RunSucceeded, Mode: "react", Goal: "list",
			Budget:  domain.Budget{MaxDurationSeconds: 60, MaxModelCalls: 5, MaxToolCalls: 5, MaxIterations: 3, MaxOutputBytes: 1000},
			Attempt: 1}
		if err := tasks.CreateRun(ctx, r); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := tasks.ListRunsByOwner(ctx, "u1", 50); err != nil {
			b.Fatal(err)
		}
	}
}

// SQLite 混合读写（文件 DB 8 连接，WAL 并发）。
func BenchmarkSQLiteMixedRW(b *testing.B) {
	db, err := store.Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	tasks := store.NewTaskRepo(db)
	ev := store.NewEventRepo(db)
	ctx := context.Background()
	ses, err := tasks.CreateSession(ctx, "u1", "bench-mixed")
	if err != nil {
		b.Fatal(err)
	}
	run := &domain.Run{ID: "run_mixed", SessionID: ses, OwnerID: "u1", Status: domain.RunQueued,
		Mode: "react", Goal: "mixed",
		Budget:  domain.Budget{MaxDurationSeconds: 60, MaxModelCalls: 5, MaxToolCalls: 5, MaxIterations: 3, MaxOutputBytes: 1000},
		Attempt: 1}
	if err := tasks.CreateRun(ctx, run); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	b.SetParallelism(8)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := ev.Append(ctx, "run_mixed", domain.EvtToolCompleted, `{"n":1}`, "normal"); err != nil {
				b.Fatal(err)
			}
			if _, err := tasks.GetRun(ctx, "u1", "run_mixed"); err != nil {
				b.Fatal(err)
			}
		}
	})
}
```

- [ ] **Step 2: 编译 + 冒烟运行**

Run: `cd backend && go vet ./tests/bench/ && go test ./tests/bench/ -run '^$' -bench 'BenchmarkExecutorIdempotentHit|BenchmarkBudgetOverBudget|BenchmarkEventsListAfter|BenchmarkListRunsByOwner|BenchmarkSQLiteMixedRW' -benchtime 10x -count=1`
Expected: 5 个基准输出 ns/op 无 panic。

- [ ] **Step 3: Commit**

```bash
git add backend/tests/bench/bench_test.go
git commit -m "test(bench): five more benchmarks — idempotent hit, budget gate, event read, run list, mixed rw"
```

---

### Task 3: 批2 验收 + docs

- [ ] **Step 1: 全量回归**

Run: `cd backend && go test ./... -count=1 -timeout 600s`
Expected: 全部 ok。

- [ ] **Step 2: evals 计数回写 README（zh/en）**

backend/README.md 评估章节 `20 个场景（工具级 12 + agent 级 8）` → `24 个场景（工具级 14 + agent 级 10）`，新增场景列表补 scenarios_round14_test.go；README.en.md 镜像（24 scenarios (14 tool-level + 10 agent-level)）。
Run: `grep -c "24 个场景\|24 scenarios" backend/README.md backend/README.en.md` → 1/1。

- [ ] **Step 3: Commit**

```bash
git add backend/README.md backend/README.en.md
git commit -m "docs(evals): scenario count 20 -> 24, bench suite notes (zh/en)"
```
