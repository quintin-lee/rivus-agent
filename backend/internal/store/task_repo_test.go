package store

import (
	"context"
	"testing"

	"rivus-agent-backend/internal/domain"
)

func setupRun(t *testing.T, tasks *TaskRepo, ses, runID string, status domain.RunStatus) {
	t.Helper()
	run := &domain.Run{ID: runID, SessionID: ses, OwnerID: "u1", Status: status,
		Mode: "react", Goal: "g",
		Budget:  domain.Budget{MaxDurationSeconds: 60, MaxModelCalls: 5, MaxToolCalls: 5, MaxIterations: 3, MaxOutputBytes: 1000},
		Attempt: 1}
	if err := tasks.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteSessionCascade(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	tasks := NewTaskRepo(db)
	events := NewEventRepo(db)
	ses, err := tasks.CreateSession(ctx, "u1", "t")
	if err != nil {
		t.Fatal(err)
	}
	setupRun(t, tasks, ses, "run_1", domain.RunQueued)
	_ = tasks.UpdateStatus(ctx, "u1", "run_1", domain.RunCancelled, "", "")
	if _, err := events.Append(ctx, "run_1", domain.EvtRunStarted, `{}`, ""); err != nil {
		t.Fatal(err)
	}
	if err := tasks.DeleteSession(ctx, "u1", ses); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := tasks.GetSession(ctx, "u1", ses); err == nil {
		t.Fatal("session must be gone")
	}
	if evs, _ := events.ListAfter(ctx, "run_1", 0, 10); len(evs) != 0 {
		t.Fatal("events must be gone")
	}
	if err := tasks.DeleteSession(ctx, "u1", ses); err == nil {
		t.Fatal("second delete must fail")
	}
}

func TestDeleteSessionRefusesActiveRun(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	tasks := NewTaskRepo(db)
	ses, _ := tasks.CreateSession(ctx, "u1", "t")
	setupRun(t, tasks, ses, "run_1", domain.RunRunning)
	if err := tasks.DeleteSession(ctx, "u1", ses); err != domain.ErrConflict {
		t.Fatalf("want ErrConflict, got %v", err)
	}
}

func TestDeleteSessionWrongOwner(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	tasks := NewTaskRepo(db)
	ses, _ := tasks.CreateSession(ctx, "u1", "t")
	if err := tasks.DeleteSession(ctx, "intruder", ses); err != domain.ErrForbidden {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
}
