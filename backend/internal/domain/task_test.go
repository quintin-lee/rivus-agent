package domain

import "testing"

func TestValidTransition(t *testing.T) {
	if RunSucceeded.Terminal() != true {
		t.Fatal("succeeded must be terminal")
	}
	if !ValidTransition(RunQueued, RunRunning) {
		t.Fatal("queued->running should be valid")
	}
	if ValidTransition(RunSucceeded, RunRunning) {
		t.Fatal("terminal must not go back to running")
	}
	if !ValidTransition(RunRunning, RunWaitingApproval) {
		t.Fatal("running->waiting_approval should be valid")
	}
	if ValidTransition(RunWaitingApproval, RunSucceeded) {
		t.Fatal("waiting_approval->succeeded must go via running")
	}
}

func TestBudgetClamp(t *testing.T) {
	server := Budget{MaxDurationSeconds: 600, MaxModelCalls: 30, MaxToolCalls: 50, MaxIterations: 12, MaxOutputBytes: 200000}
	want := Budget{MaxDurationSeconds: 9999, MaxModelCalls: 999, MaxToolCalls: 1, MaxIterations: 0, MaxOutputBytes: 10}
	got := want.ClampByServer(server)
	if got.MaxDurationSeconds != 600 || got.MaxModelCalls != 30 || got.MaxToolCalls != 1 || got.MaxIterations != 12 || got.MaxOutputBytes != 10 {
		t.Fatalf("clamp wrong: %+v", got)
	}
}
