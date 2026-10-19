package integration

import (
	"context"
	"testing"
	"time"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/tests/testutil"
)

func TestTaskToolDelegatesReadOnlyChild(t *testing.T) {
	h := testutil.New(t)
	// 共享 ScriptedModel 的 FIFO queue（父/子同 model）：
	//   父 call1 = 调 task；子 call1 = 子 final；父 call2 = 父 final
	h.Model.EnqueueToolCall("tc", "task", `{"goal":"check the handbook","tools":["file_read"]}`)
	h.Model.EnqueueText("child final answer")
	h.Model.EnqueueText("parent final done")
	ses := h.CreateSession("s")
	id, code, _ := h.CreateRun(ses, fixturePayload(t, ses), "sub-1")
	if code != 201 {
		t.Fatalf("create run: %d", code)
	}
	run := h.WaitTerminal(id, 30*time.Second)
	// 父成功（子结果已并入父模型）
	if run.Status != domain.RunSucceeded {
		t.Fatalf("parent want succeeded got %s (%s)", run.Status, run.ErrorSummary)
	}
	// 子行存在且 terminal、parent_run_id=父
	children, err := h.Tasks.ListChildrenByRun(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 1 {
		t.Fatalf("want 1 child, got %d", len(children))
	}
	child := children[0]
	if child.ParentRunID != id {
		t.Fatalf("child.parent=%q want %q", child.ParentRunID, id)
	}
	if !child.Status.Terminal() {
		t.Fatalf("child not terminal: %s", child.Status)
	}
	if child.Budget.MaxModelCalls > childCapForTest(run.Budget.MaxModelCalls) {
		t.Fatalf("child budget not clamped: %+v", child.Budget)
	}
	// 事件流含子 runID 的 run.started/run.finished
	evs, _ := h.Events.ListAfter(context.Background(), child.ID, 0, 1000)
	var sawStart, sawFinish bool
	for _, e := range evs {
		if e.Type == domain.EvtRunStarted {
			sawStart = true
		}
		if e.Type == domain.EvtRunFinished {
			sawFinish = true
		}
	}
	if !sawStart || !sawFinish {
		t.Fatalf("child events missing: start=%v finish=%v", sawStart, sawFinish)
	}
}

func TestTaskToolReadOnlyInvariant(t *testing.T) {
	h := testutil.New(t)
	h.Model.EnqueueToolCall("tc", "task", `{"goal":"x","tools":["workspace_write"]}`)
	h.Model.EnqueueText("parent done")
	ses := h.CreateSession("s")
	id, _, _ := h.CreateRun(ses, fixturePayload(t, ses), "sub-2")
	_ = h.WaitTerminal(id, 30*time.Second)
	if kids, _ := h.Tasks.ListChildrenByRun(context.Background(), id); len(kids) != 0 {
		t.Fatalf("read-only invariant violated: child rows created %+v", kids)
	}
}

// childCapForTest 镜像 subagent.clampChildBudget：40% 取整 + 命名上限（委派用例无请求覆盖）。
func childCapForTest(parentMMC int) int {
	v := int(float64(parentMMC) * 0.40)
	const capMMC = 20
	if v > capMMC {
		return capMMC
	}
	return v
}
