package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/store"
)

func newExportDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "exp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	tasks := store.NewTaskRepo(db)
	events := store.NewEventRepo(db)
	memories := store.NewMemoryRepo(db)

	ses, err := tasks.CreateSession(ctx, "u1", "exp-session")
	if err != nil {
		t.Fatal(err)
	}
	run := &domain.Run{ID: "run_1", SessionID: ses, OwnerID: "u1", Status: domain.RunSucceeded,
		Mode: "react", Goal: "g1",
		Budget:  domain.Budget{MaxDurationSeconds: 60, MaxModelCalls: 5, MaxToolCalls: 5, MaxIterations: 3, MaxOutputBytes: 1000},
		Attempt: 1}
	if err := tasks.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, err := events.Append(ctx, "run_1", domain.EvtRunStarted, `{}`, ""); err != nil {
		t.Fatal(err)
	}
	if err := memories.Write(ctx, &domain.Memory{SessionID: ses, OwnerID: "u1", Content: "m1"}); err != nil {
		t.Fatal(err)
	}
	return db, ses
}

func TestBuildExportPayload(t *testing.T) {
	db, ses := newExportDB(t)

	payload, err := buildExportPayload(db, ses)
	if err != nil {
		t.Fatal(err)
	}
	if payload.SessionID != ses {
		t.Fatalf("session_id = %q, want %q", payload.SessionID, ses)
	}
	if len(payload.Runs) != 1 || payload.Runs[0].ID != "run_1" || payload.Runs[0].Goal != "g1" {
		t.Fatalf("runs mismatch: %+v", payload.Runs)
	}
	if len(payload.Events) != 1 || payload.Events[0].Type != domain.EvtRunStarted {
		t.Fatalf("events mismatch: %+v", payload.Events)
	}
	if len(payload.Memories) != 1 || payload.Memories[0].Content != "m1" {
		t.Fatalf("memories mismatch: %+v", payload.Memories)
	}
	if _, err := json.Marshal(payload); err != nil {
		t.Fatalf("payload not marshalable: %v", err)
	}
}

func TestBuildExportPayloadMissingSession(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "missing.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := buildExportPayload(db, "ses_nonexistent"); err == nil {
		t.Fatal("expected error for missing session")
	} else if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error should mention not found: %v", err)
	}
}
