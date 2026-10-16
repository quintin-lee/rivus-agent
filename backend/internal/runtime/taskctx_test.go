package runtime

import (
	"context"
	"testing"

	"rivus-agent-backend/internal/domain"
)

func TestTaskContextRoundTrip(t *testing.T) {
	base := context.Background()
	if _, ok := TaskContextFrom(base); ok {
		t.Fatal("absent ctx must not report ok")
	}
	tc := TaskContext{OwnerID: "o1", ParentRunID: "run_x", SessionID: "ses_y", ParentBudget: domain.Budget{MaxModelCalls: 5}, Depth: 0}
	ctx := WithTaskContext(base, tc)
	got, ok := TaskContextFrom(ctx)
	if !ok {
		t.Fatal("with task ctx must report ok")
	}
	if got != tc {
		t.Fatalf("got %+v want %+v", got, tc)
	}
}
