package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/eino/adk"
	"rivus-agent-backend/internal/domain"
)

// driveDrain runs drain synchronously against a scripted iterator and returns
// the events captured on out. drain's emit only drops when out is full, so a
// sufficiently large buffer guarantees nothing is lost without a reader.
func driveDrain(t *testing.T, r *EinoRunner, events []*adk.AgentEvent) []RuntimeEvent {
	t.Helper()
	iter, gen := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	for _, ev := range events {
		gen.Send(ev)
	}
	gen.Close()

	out := make(chan RuntimeEvent, 16)
	r.drain(context.Background(), "run-1", "", iter, out, false)
	close(out)

	var got []RuntimeEvent
	for ev := range out {
		got = append(got, ev)
	}
	return got
}

func TestDrainToolFailedEmitsEvtToolFailed(t *testing.T) {
	r := NewEinoRunner(nil, nil, nil, nil)

	// A tool call is seen first (so lastTool is tracked as a hint), then the
	// model surfaces the executor's tool_failed CodedError.
	toolEvent := &adk.AgentEvent{
		Output: &adk.AgentOutput{
			MessageOutput: &adk.MessageVariant{
				ToolName: "file_read",
			},
		},
	}
	failErr := domain.Wrap("tool_failed", "tool failed", false, errors.New("boom"))
	failEvent := &adk.AgentEvent{Err: failErr}

	got := driveDrain(t, r, []*adk.AgentEvent{toolEvent, failEvent})

	var toolFailedIdx, toolCompletedIdx, runFailedIdx, modelFailedIdx = -1, -1, -1, -1
	for i, ev := range got {
		switch ev.Type {
		case domain.EvtToolCompleted:
			toolCompletedIdx = i
		case domain.EvtModelFailed:
			modelFailedIdx = i
		case domain.EvtToolFailed:
			toolFailedIdx = i
		case domain.EvtRunFailed:
			runFailedIdx = i
		}
	}

	if toolFailedIdx == -1 {
		t.Fatalf("expected EvtToolFailed in drain output, got events: %+v", got)
	}
	if runFailedIdx == -1 {
		t.Fatalf("expected terminal EvtRunFailed after EvtToolFailed, got events: %+v", got)
	}
	if toolFailedIdx >= runFailedIdx {
		t.Fatalf("EvtToolFailed (idx %d) must precede EvtRunFailed (idx %d): %+v", toolFailedIdx, runFailedIdx, got)
	}
	if toolCompletedIdx != -1 && toolFailedIdx < toolCompletedIdx {
		t.Fatalf("EvtToolFailed must come after the tool call that caused it; got events: %+v", got)
	}
	// Existing run-fail behavior is unchanged: EvtModelFailed still precedes EvtRunFailed.
	if modelFailedIdx == -1 {
		t.Fatalf("expected EvtModelFailed in drain output, got events: %+v", got)
	}
	if !(modelFailedIdx < runFailedIdx) {
		t.Fatalf("EvtModelFailed (idx %d) must precede EvtRunFailed (idx %d): %+v", modelFailedIdx, runFailedIdx, got)
	}
	// Spec §Task10: EvtToolFailed is emitted BEFORE the existing run-fail
	// events (EvtModelFailed + EvtRunFailed) in the same drain branch.
	if toolFailedIdx >= modelFailedIdx {
		t.Fatalf("EvtToolFailed (idx %d) must precede EvtModelFailed (idx %d): %+v", toolFailedIdx, modelFailedIdx, got)
	}

	tf := got[toolFailedIdx]
	if tf.ToolName != "file_read" {
		t.Errorf("EvtToolFailed.ToolName = %q, want best-effort hint %q", tf.ToolName, "file_read")
	}
	if tf.Text != failErr.Error() {
		t.Errorf("EvtToolFailed.Text = %q, want %q", tf.Text, failErr.Error())
	}
}

func TestDrainToolFailedNoPriorToolHasEmptyHint(t *testing.T) {
	r := NewEinoRunner(nil, nil, nil, nil)
	failErr := domain.Wrap("tool_failed", "tool failed", false, errors.New("nope"))
	got := driveDrain(t, r, []*adk.AgentEvent{{Err: failErr}})

	var toolFailed *RuntimeEvent
	for i := range got {
		if got[i].Type == domain.EvtToolFailed {
			toolFailed = &got[i]
		}
	}
	if toolFailed == nil {
		t.Fatalf("expected EvtToolFailed, got: %+v", got)
	}
	if toolFailed.ToolName != "" {
		t.Errorf("EvtToolFailed.ToolName = %q, want empty hint when no tool call was seen", toolFailed.ToolName)
	}
}

// Non tool-failure errors must NOT emit EvtToolFailed (the existing
// EvtModelFailed + EvtRunFailed path is preserved untouched).
func TestDrainNonToolErrorDoesNotEmitEvtToolFailed(t *testing.T) {
	r := NewEinoRunner(nil, nil, nil, nil)
	got := driveDrain(t, r, []*adk.AgentEvent{{Err: errors.New("plain")}})

	var runFailed bool
	for _, ev := range got {
		switch ev.Type {
		case domain.EvtToolFailed:
			t.Fatalf("unexpected EvtToolFailed for non tool_failed error: %+v", ev)
		case domain.EvtRunFailed:
			runFailed = true
		}
	}
	if !runFailed {
		t.Fatalf("expected EvtRunFailed to still be emitted, got: %+v", got)
	}
}
