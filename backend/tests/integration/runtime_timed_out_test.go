package integration

import (
	"context"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/tests/testutil"
)

func TestRunTimedOut(t *testing.T) {
	h := testutil.New(t)
	// model blocks 2s, exceeding the 1s run deadline
	h.Model.EnqueueFunc(func(_ int, _ []*schema.Message) (*schema.Message, error) {
		time.Sleep(2 * time.Second)
		return &schema.Message{Role: schema.Assistant, Content: "never reached"}, nil
	})
	ses := h.CreateSession("s")
	id, code, _ := h.CreateRun(ses, map[string]any{
		"goal":   "block until timeout",
		"budget": map[string]any{"max_duration_seconds": 1},
	}, "timeout-1")
	if code != 201 {
		t.Fatalf("create run: %d", code)
	}
	run := h.WaitTerminal(id, 15*time.Second)
	if run.Status != domain.RunTimedOut {
		t.Fatalf("want timed_out, got %s (%s)", run.Status, run.ErrorSummary)
	}
	if run.ErrorCode != "timed_out" {
		t.Fatalf("want error_code timed_out, got %q", run.ErrorCode)
	}
	evs, err := h.Events.ListAfter(context.Background(), id, 0, 1000)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	for _, e := range evs {
		if e.Type == domain.EvtRunTimedOut {
			return // run.timed_out persisted (written via context.Background())
		}
	}
	t.Fatalf("run.timed_out event not persisted")
}
