package store

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	"rivus-agent-backend/internal/domain"
)

// seedRun 建 session + run "run_1"（agent_events.run_id FK → runs.id，Open 开了 foreign_keys=ON）。
func seedRun(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	tasks := NewTaskRepo(db)
	ses, err := tasks.CreateSession(ctx, "u1", "t")
	if err != nil {
		t.Fatal(err)
	}
	run := &domain.Run{ID: "run_1", SessionID: ses, OwnerID: "u1", Status: domain.RunQueued,
		Mode: "react", Goal: "g", Budget: domain.Budget{}, Attempt: 1}
	if err := tasks.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
}

// A SetPublisher hook fires once per Append with the persisted event shape.
func TestEventRepoPublisherFiresOnAppend(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seedRun(t, db)
	repo := NewEventRepo(db)

	var mu sync.Mutex
	var got []domain.AgentEvent
	repo.SetPublisher(func(ev domain.AgentEvent) {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
	})

	seq, err := repo.Append(context.Background(), "run_1", domain.EvtModelToken, `{"text":"hi"}`, "")
	if err != nil {
		t.Fatal(err)
	}
	if seq == 0 {
		t.Fatal("expected non-zero seq")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("expected 1 published event, got %d", len(got))
	}
	ev := got[0]
	if ev.Seq != seq || ev.RunID != "run_1" || ev.Type != domain.EvtModelToken ||
		ev.PayloadJSON != `{"text":"hi"}` || ev.Sensitivity != "normal" {
		t.Fatalf("published event mismatch: %+v (want seq=%d, run=run_1, type=%s, payload=%q, sens=normal)",
			ev, seq, domain.EvtModelToken, `{"text":"hi"}`)
	}
}

// With no SetPublisher, Append still works and publishes nothing (nil-safe).
func TestEventRepoNilPublisherNoop(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seedRun(t, db)
	repo := NewEventRepo(db)
	if _, err := repo.Append(context.Background(), "run_1", domain.EvtRunStarted, `{}`, ""); err != nil {
		t.Fatal(err)
	}
	// No panic, no side effects — just confirm the row persisted.
	list, err := repo.ListAfter(context.Background(), "run_1", 0, 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("expected 1 row, got %d err=%v", len(list), err)
	}
}
