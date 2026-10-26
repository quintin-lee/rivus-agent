package integration

import (
	"context"
	"testing"
	"time"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/tests/testutil"
)

// 并发=1 使两个子按输入顺序串行消费共享 ScriptedModel FIFO → 断言确定性。
func TestTasksToolTwoChildrenSerial(t *testing.T) {
	h := testutil.New(t)
	h.Model.EnqueueToolCall("tc", "tasks", `{"tasks":[{"goal":"g1","tools":["file_read"]},{"goal":"g2","tools":["file_read"]}],"max_concurrency":1}`)
	h.Model.EnqueueText("child one final")   // 子1
	h.Model.EnqueueText("child two final")   // 子2
	h.Model.EnqueueText("parent final done") // 父
	ses := h.CreateSession("s")
	id, code, _ := h.CreateRun(ses, fixturePayload(t, ses), "batch-1")
	if code != 201 {
		t.Fatalf("create run: %d", code)
	}
	run := h.WaitTerminal(id, 30*time.Second)
	if run.Status != domain.RunSucceeded {
		t.Fatalf("parent want succeeded got %s (%s)", run.Status, run.ErrorSummary)
	}
	kids, err := h.Tasks.ListChildrenByRun(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(kids) != 2 {
		t.Fatalf("want 2 children, got %d", len(kids))
	}
	saw := map[string]bool{}
	for _, c := range kids {
		if !c.Status.Terminal() {
			t.Fatalf("child %s not terminal: %s", c.ID, c.Status)
		}
		saw[c.Goal] = true
		evs, _ := h.Events.ListAfter(context.Background(), c.ID, 0, 500)
		var start, finish bool
		for _, e := range evs {
			if e.Type == domain.EvtRunStarted {
				start = true
			}
			if e.Type == domain.EvtRunFinished {
				finish = true
			}
		}
		if !start || !finish {
			t.Fatalf("child %s events missing: start=%v finish=%v", c.ID, start, finish)
		}
	}
	if !saw["g1"] || !saw["g2"] {
		t.Fatalf("children goals wrong: %v", saw)
	}
}
