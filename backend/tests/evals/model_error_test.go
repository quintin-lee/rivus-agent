package evals

import (
	"errors"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/tests/testutil"
)

func TestEvalModelErrorFails(t *testing.T) {
	h := testutil.New(t)
	h.Model.SetFallback(func(int, []*schema.Message) (*schema.Message, error) {
		return nil, errors.New("connection refused")
	})
	ses := h.CreateSession("eval")
	id, _, _ := h.CreateRun(ses, map[string]any{"goal": "hi"}, "eval-model-err")
	run := h.WaitTerminal(id, 30*time.Second)
	if run.Status != domain.RunFailed {
		t.Fatalf("model error must fail the run, got %s (%s)", run.Status, run.ResultJSON)
	}
}
