package integration

import (
	"testing"
	"time"

	"rivus-agent-backend/tests/testutil"
)

func TestRetryFailedRun(t *testing.T) {
	h := testutil.New(t)
	h.Model.EnqueueText("nothing useful")
	ses := h.CreateSession("retry")
	id, code, _ := h.CreateRun(ses, map[string]any{
		"goal": "do thing", "success_criteria": []string{"impossible criterion xyz"},
	}, "retry-1")
	if code != 201 {
		t.Fatalf("create: %d", code)
	}
	first := h.WaitTerminal(id, 30*time.Second)
	if first.Status != "failed" {
		t.Fatalf("want failed, got %s", first.Status)
	}
	if first.Attempt != 1 {
		t.Fatalf("want attempt 1, got %d", first.Attempt)
	}
	h.Model.EnqueueText("impossible criterion xyz satisfied with evidence")
	code, _ = h.Do("POST", "/api/v1/runs/"+id+"/resume", nil, nil)
	if code != 200 {
		t.Fatalf("resume: %d", code)
	}
	second := h.WaitTerminal(id, 30*time.Second)
	if second.Status != "succeeded" || second.Attempt != 2 {
		t.Fatalf("want succeeded attempt 2, got %s attempt %d (%s)", second.Status, second.Attempt, second.ErrorSummary)
	}
}

func TestResumeRefusesCancelled(t *testing.T) {
	h := testutil.New(t, blockToolDef())
	h.Model.EnqueueToolCall("tc1", "block_tool", `{}`)
	ses := h.CreateSession("retry3")
	id, _, _ := h.CreateRun(ses, map[string]any{"goal": "slow"}, "retry-3")
	time.Sleep(500 * time.Millisecond)
	h.Do("POST", "/api/v1/runs/"+id+"/cancel", nil, nil)
	h.WaitTerminal(id, 30*time.Second)
	if code, _ := h.Do("POST", "/api/v1/runs/"+id+"/resume", nil, nil); code != 409 {
		t.Fatalf("cancelled resume must 409, got %d", code)
	}
}
