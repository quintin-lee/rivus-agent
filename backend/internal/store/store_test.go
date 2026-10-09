package store

import (
	"context"
	"testing"

	"rivus-agent-backend/internal/domain"
)

func TestCRUD(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	tasks := NewTaskRepo(db)
	events := NewEventRepo(db)
	approvals := NewApprovalRepo(db)
	cps := NewCheckpointStore(db)

	ses, err := tasks.CreateSession(ctx, "u1", "t")
	if err != nil {
		t.Fatal(err)
	}
	run := &domain.Run{ID: "run_1", SessionID: ses, OwnerID: "u1", Status: domain.RunQueued,
		Mode: "react", Goal: "g", Budget: domain.Budget{MaxDurationSeconds: 60, MaxModelCalls: 5, MaxToolCalls: 5, MaxIterations: 3, MaxOutputBytes: 1000}, Attempt: 1}
	if err := tasks.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.FindByIdempotencyKey(ctx, "u1", ""); err == nil {
		t.Fatal("empty key must miss")
	}
	if err := tasks.MarkStarted(ctx, "u1", "run_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := events.Append(ctx, "run_1", domain.EvtRunStarted, `{}`, ""); err != nil {
		t.Fatal(err)
	}
	list, err := events.ListAfter(ctx, "run_1", 0, 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("events wrong: %v %d", err, len(list))
	}
	if err := cps.Set(ctx, "ckpt_run_1", []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	b, ok, err := cps.Get(ctx, "ckpt_run_1")
	if err != nil || !ok || len(b) != 3 {
		t.Fatalf("checkpoint wrong: %v %v %d", err, ok, len(b))
	}
	a := &domain.Approval{RunID: "run_1", ToolCallID: "tc_1", ToolName: "workspace_write", ArgsHash: "h", RequestedBy: "u1", ExpiresAt: 9999999999999}
	if err := approvals.Create(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, err := approvals.FindPendingByToolCall(ctx, "run_1", "tc_1"); err != nil {
		t.Fatal(err)
	}
	if err := approvals.Decide(ctx, a.ID, true, "u1", "ok"); err != nil {
		t.Fatal(err)
	}
	if err := tasks.UpdateStatus(ctx, "u1", "run_1", domain.RunSucceeded, "", ""); err != nil {
		t.Fatal(err)
	}
	got, err := tasks.GetRun(ctx, "u1", "run_1")
	if err != nil || got.Status != domain.RunSucceeded {
		t.Fatalf("run status wrong: %v %+v", err, got)
	}
	if err := tasks.UpdateStatus(ctx, "u1", "run_1", domain.RunRunning, "", ""); err == nil {
		t.Fatal("terminal must not transition")
	}
}
