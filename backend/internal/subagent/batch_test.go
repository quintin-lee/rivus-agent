package subagent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/runtime"
	"rivus-agent-backend/internal/store"
	itool "rivus-agent-backend/internal/tool"
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

func (f *fakeRunner) snapshot(t *testing.T) []runtime.ChildRequest {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]runtime.ChildRequest(nil), f.calls...)
}

type fakeCleaner struct{ deleted []string }

func (c *fakeCleaner) DeleteCheckpointsByRun(_ context.Context, runID string) {
	c.deleted = append(c.deleted, runID)
}

// newBatchEnv：文件库 + 真实 TaskRepo（runOneChild 的 DB 路径全真；不用 :memory: —— 每连接分库陷阱）。
// 返回真实会话 ID：runs.session_id 有 FK → sessions(id)，tc.SessionID 必须真实存在。
func newBatchEnv(t *testing.T) (childDeps, *store.TaskRepo, string) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "batch.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	tasks := store.NewTaskRepo(db)
	sesID, err := tasks.CreateSession(context.Background(), "o1", "batch")
	if err != nil {
		t.Fatal(err)
	}
	return childDeps{tasks: tasks}, tasks, sesID
}

func batchTC(sesID string) runtime.TaskContext {
	return runtime.TaskContext{OwnerID: "o1", ParentRunID: "p1", SessionID: sesID,
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

func newEmptyRegistry(t *testing.T) *itool.Registry {
	t.Helper()
	return itool.NewRegistry()
}

func mustChildren(t *testing.T, tasks *store.TaskRepo, parent string) []domain.Run {
	t.Helper()
	kids, err := tasks.ListChildrenByRun(context.Background(), parent)
	if err != nil {
		t.Fatal(err)
	}
	return kids
}

func TestTasksHandlerResultsInInputOrder(t *testing.T) {
	deps, tasks, sesID := newBatchEnv(t)
	runner := &fakeRunner{}
	deps.runner = runner
	deps.reg = newEmptyRegistry(t)
	h := NewTasksHandler(deps.reg, deps.tasks, runner, &fakeCleaner{})
	in := mustJSON(t, batchIn{Tasks: []taskIn{{Goal: "g1"}, {Goal: "g2"}, {Goal: "g3"}}})
	out, err := h(runtime.WithTaskContext(context.Background(), batchTC(sesID)), in)
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
	for _, c := range mustChildren(t, tasks, "p1") {
		if c.Status != domain.RunSucceeded {
			t.Fatalf("child %s status=%s want succeeded", c.ID, c.Status)
		}
	}
}

func TestTasksHandlerConcurrencyBound(t *testing.T) {
	deps, _, sesID := newBatchEnv(t)
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
	if _, err := h(runtime.WithTaskContext(context.Background(), batchTC(sesID)), in); err != nil {
		t.Fatal(err)
	}
	got := atomic.LoadInt64(&maxInFlight)
	if got != 2 {
		t.Fatalf("max in-flight=%d want exactly 2 (default would be 4; serial would be 1)", got)
	}
}

func TestTasksHandlerValidation(t *testing.T) {
	deps, tasks, sesID := newBatchEnv(t)
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
		{"empty batch", batchTC(sesID), batchIn{}},
		{"too many", batchTC(sesID), batchIn{Tasks: []taskIn{{Goal: "1"}, {Goal: "2"}, {Goal: "3"}, {Goal: "4"}, {Goal: "5"}, {Goal: "6"}}}},
		{"empty goal", batchTC(sesID), batchIn{Tasks: []taskIn{{Goal: "ok"}, {Goal: "  "}}}},
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
	if kids := mustChildren(t, tasks, "p1"); len(kids) != 0 {
		t.Fatalf("validation failures must not create children: %v", kids)
	}
}
