package bench

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"rivus-agent-backend/internal/domain"
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
