package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/tests/testutil"
)

// TestModelTokenPersists proves the new EvtModelToken event is emitted by the
// parent-run streaming path AND persisted through the OnEvent → Events.Append
// bridge (no new plumbing) so it reaches the SSE/ListAfter replay path.
func TestModelTokenPersists(t *testing.T) {
	h := testutil.New(t)
	h.Model.EnqueueText("streamed answer")
	ses := h.CreateSession("s")
	id, code, _ := h.CreateRun(ses, map[string]any{"goal": "hi"}, "model-token-1")
	if code != 201 {
		t.Fatalf("create run: %d", code)
	}
	_ = h.WaitTerminal(id, 30*time.Second)

	evs, err := h.Events.ListAfter(context.Background(), id, 0, 500)
	if err != nil {
		t.Fatal(err)
	}

	// 1) A terminal model.completed event must exist (the existing behavior).
	var sawCompleted bool
	for _, e := range evs {
		if e.Type == domain.EvtModelCompleted {
			sawCompleted = true
		}
	}
	if !sawCompleted {
		t.Fatalf("no model.completed among %d events — baseline streaming path broken", len(evs))
	}

	// 2) The new model.token event must be persisted (proves the OnEvent bridge).
	var tokenPayloads []string
	for _, e := range evs {
		if e.Type == domain.EvtModelToken {
			tokenPayloads = append(tokenPayloads, e.PayloadJSON)
		}
	}
	if len(tokenPayloads) == 0 {
		t.Fatalf("no model.token persisted among %d events — expected ≥1 from the streaming parent run", len(evs))
	}

	// 3) model.token payload is full-so-far: the LAST token's text contains the
	//    answer, matching the model.completed full text (idempotent).
	last := tokenPayloads[len(tokenPayloads)-1]
	if !strings.Contains(last, "streamed answer") {
		t.Fatalf("last model.token payload %q does not contain the full answer (full-so-far broken)", last)
	}
}
