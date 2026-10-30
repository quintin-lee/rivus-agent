package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"rivus-agent-backend/internal/config"
	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/store"
)

func newPruneDB(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("AGENT_DB_PATH", filepath.Join(dir, "prune.db"))
	if _, err := config.Load(""); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(dir, "prune.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	tasks := store.NewTaskRepo(db)
	ses, err := tasks.CreateSession(ctx, "u1", "prune-ses")
	if err != nil {
		t.Fatal(err)
	}
	run := &domain.Run{
		ID: "run_prune", SessionID: ses, OwnerID: "u1", Status: domain.RunSucceeded,
		Mode: "react", Goal: "g",
		Budget:  domain.Budget{MaxDurationSeconds: 60, MaxModelCalls: 5, MaxToolCalls: 5, MaxIterations: 3, MaxOutputBytes: 1000},
		Attempt: 1,
	}
	if err := tasks.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "prune.db"), run.ID
}

func TestPruneCoreDeletesOldTerminalRuns(t *testing.T) {
	dbPath, runID := newPruneDB(t)
	if err := pruneCore([]string{"-older-than", "0s"}); err != nil {
		t.Fatalf("pruneCore: %v", err)
	}
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM runs WHERE id = ?`, runID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("run still present after prune")
	}
}

func TestPruneCoreRequiresOlderThan(t *testing.T) {
	if _, runID := newPruneDB(t); runID == "" {
		t.Fatal("seed failed")
	}
	err := pruneCore([]string{})
	if err == nil {
		t.Fatal("expected error without -older-than")
	}
	if err != errPruneUsage {
		t.Fatalf("err = %v, want errPruneUsage", err)
	}
}

func TestPruneCoreDryRunKeepsRuns(t *testing.T) {
	dbPath, runID := newPruneDB(t)
	if err := pruneCore([]string{"-older-than", "0s", "-dry-run"}); err != nil {
		t.Fatalf("pruneCore: %v", err)
	}
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM runs WHERE id = ?`, runID).Scan(&n); err != nil {
		if err != sql.ErrNoRows {
			t.Fatal(err)
		}
	}
	if n != 1 {
		t.Fatalf("dry-run deleted the run")
	}
	_ = os.Remove(dbPath)
	_ = time.Now()
}
