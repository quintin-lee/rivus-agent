package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/runtime"
	"rivus-agent-backend/internal/store"
	"rivus-agent-backend/internal/tool"
	"rivus-agent-backend/internal/tool/builtin"
	"rivus-agent-backend/tests/testutil"
)

func BenchmarkEventAppend(b *testing.B) {
	h := testutil.New(b)
	db, err := store.Open(":memory:")
	if err != nil {
		b.Fatal(err)
	}
	ev := store.NewEventRepo(db)
	tasks := store.NewTaskRepo(db)
	ctx := context.Background()
	ses, err := tasks.CreateSession(ctx, "u1", "bench")
	if err != nil {
		b.Fatal(err)
	}
	run := &domain.Run{ID: "run_bench", SessionID: ses, OwnerID: "u1", Status: domain.RunQueued,
		Mode: "react", Goal: "bench",
		Budget:  domain.Budget{MaxDurationSeconds: 60, MaxModelCalls: 5, MaxToolCalls: 5, MaxIterations: 3, MaxOutputBytes: 1000},
		Attempt: 1,
	}
	if err := tasks.CreateRun(ctx, run); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ev.Append(ctx, "run_bench", domain.EvtToolCompleted, `{"n":1}`, "normal"); err != nil {
			b.Fatal(err)
		}
	}
	_ = h
}

func BenchmarkToolExecute(b *testing.B) {
	reg := tool.NewRegistry()
	if err := builtin.RegisterAll(reg); err != nil {
		b.Fatal(err)
	}
	ex := tool.NewExecutor(reg)
	ctx := context.Background()
	args := json.RawMessage(`{"query":"bench","top_k":3}`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ex.Execute(ctx, tool.ExecRequest{RunID: "r", ToolName: "knowledge_search", Args: args}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAPICreateRun(b *testing.B) {
	h := testutil.New(b)
	ses := h.CreateSession("bench")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, code, _ := h.CreateRun(ses, map[string]any{"goal": "bench"}, "")
		if code != 201 {
			b.Fatalf("create run: %d", code)
		}
	}
}

func TestLoadConcurrentCreateRuns(t *testing.T) {
	h := testutil.New(t)
	ses := h.CreateSession("load")
	const n = 20
	ids := make([]string, n)
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id, code, _ := h.CreateRun(ses, map[string]any{"goal": "load"}, "")
			if code != 201 {
				t.Errorf("create run %d: %d", i, code)
				return
			}
			ids[i] = id
		}(i)
	}
	wg.Wait()
	for _, id := range ids {
		if id == "" {
			continue
		}
		if run := h.WaitTerminal(id, 60*time.Second); run.Status != domain.RunSucceeded {
			t.Errorf("run %s: %s", id, run.Status)
		}
	}
	t.Logf("%d concurrent runs finished in %v", n, time.Since(start))
}

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

// benchRun 构造一条可落库的 run（FK：需先建 session）。
func benchRun(b *testing.B, tasks *store.TaskRepo, ctx context.Context, ses, id, goal string) {
	b.Helper()
	r := &domain.Run{ID: id, SessionID: ses, OwnerID: "u1", Status: domain.RunQueued,
		Mode: "react", Goal: goal,
		Budget:  domain.Budget{MaxDurationSeconds: 60, MaxModelCalls: 5, MaxToolCalls: 5, MaxIterations: 3, MaxOutputBytes: 1000},
		Attempt: 1}
	if err := tasks.CreateRun(ctx, r); err != nil {
		b.Fatal(err)
	}
}

// 事件读：预写 100 条后 ListAfter 全量读（文件 DB，8 连接）。
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
	benchRun(b, tasks, ctx, ses, "run_read", "read")
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
		benchRun(b, tasks, ctx, ses, fmt.Sprintf("run_list_%02d", i), "list")
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := tasks.ListRunsByOwner(ctx, "u1", 50); err != nil {
			b.Fatal(err)
		}
	}
}

// SQLite 混合读写（文件 DB 8 连接，WAL 并发，8 worker）。
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
	benchRun(b, tasks, ctx, ses, "run_mixed", "mixed")
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
