package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"rivus-agent-backend/internal/domain"
)

// TestDrainStreamCoalesces: 3 chunks → onDelta called with full-so-far text
// (windowed), and drainStream returns the concatenated full text.
func TestDrainStreamCoalesces(t *testing.T) {
	sr, sw := schema.Pipe[*schema.Message](4)
	go func() {
		defer sw.Close()
		for _, word := range []string{"h", "el", "lo"} {
			sw.Send(&schema.Message{Content: word}, nil)
		}
	}()

	var deltas []string
	onDelta := func(textSoFar string) { deltas = append(deltas, textSoFar) }

	// Make the window tiny so the test is deterministic (no real sleeps).
	// NOTE: tokenFlushInterval is a package global; these tests MUST stay
	// sequential (no t.Parallel()) because they mutate it. Restore it after.
	orig := tokenFlushInterval
	tokenFlushInterval = 1 * time.Millisecond
	defer func() { tokenFlushInterval = orig }()

	full := drainStream(sr, onDelta)

	if full != "hello" {
		t.Fatalf("drainStream full = %q; want %q", full, "hello")
	}
	if len(deltas) == 0 {
		t.Fatal("drainStream: expected ≥1 onDelta call")
	}
	// Full-so-far invariant: each delta is a prefix of the next; last delta ≤ full.
	for i := 1; i < len(deltas); i++ {
		if !strings.HasPrefix(deltas[i], deltas[i-1]) {
			t.Fatalf("deltas not full-so-far: %q → %q", deltas[i-1], deltas[i])
		}
	}
	if !strings.HasPrefix(full, deltas[len(deltas)-1]) {
		t.Fatalf("last delta %q is not a prefix of full %q", deltas[len(deltas)-1], full)
	}
	// NOTE: by design there is NO final flush of the last <window tail. The
	// full text is delivered by EvtModelCompleted; do not "fix" a tail flush.
}

// TestDrainStreamNilOnDelta: onDelta nil → no deltas, full text still returned.
func TestDrainStreamNilOnDelta(t *testing.T) {
	sr, sw := schema.Pipe[*schema.Message](2)
	go func() {
		defer sw.Close()
		sw.Send(&schema.Message{Content: "x"}, nil)
		sw.Send(&schema.Message{Content: "y"}, nil)
	}()
	full := drainStream(sr, nil)
	if full != "xy" {
		t.Fatalf("drainStream(nil) full = %q; want %q", full, "xy")
	}
}

// TestDrainStreamGate: stream=true → drain emits EvtModelToken; stream=false → not emitted.
func TestDrainStreamGate(t *testing.T) {
	r := NewEinoRunner(nil, nil, nil, nil)

	// Each drain call consumes + closes its own stream reader, so build a fresh
	// event/pipe per call; reusing one reader across two drains double-closes it.
	evParent := &adk.AgentEvent{
		Output: &adk.AgentOutput{
			MessageOutput: &adk.MessageVariant{
				MessageStream: buildStreamPipe("hi there"),
			},
		},
	}
	evChild := &adk.AgentEvent{
		Output: &adk.AgentOutput{
			MessageOutput: &adk.MessageVariant{
				MessageStream: buildStreamPipe("hi there"),
			},
		},
	}

	gotParent := drainStreamed(t, r, []*adk.AgentEvent{evParent}, true)
	var hasToken, hasCompleted bool
	for _, re := range gotParent {
		switch re.Type {
		case domain.EvtModelToken:
			hasToken = true
		case domain.EvtModelCompleted:
			hasCompleted = true
		}
	}
	if !hasToken {
		t.Error("stream=true: expected EvtModelToken in drain output")
	}
	if !hasCompleted {
		t.Error("stream=true: expected EvtModelCompleted terminal")
	}

	gotChild := drainStreamed(t, r, []*adk.AgentEvent{evChild}, false)
	for _, re := range gotChild {
		if re.Type == domain.EvtModelToken {
			t.Error("stream=false: did not expect EvtModelToken")
		}
	}
}

// drainStreamed: like driveDrain (in runner_failed_tool_test.go) but parameterized on `stream`.
func drainStreamed(t *testing.T, r *EinoRunner, events []*adk.AgentEvent, stream bool) []RuntimeEvent {
	t.Helper()
	iter, gen := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	for _, e := range events {
		gen.Send(e)
	}
	gen.Close()
	out := make(chan RuntimeEvent, 16)
	r.drain(context.Background(), "run-1", "", iter, out, stream)
	close(out)
	var got []RuntimeEvent
	for re := range out {
		got = append(got, re)
	}
	return got
}

// buildStreamPipe: build a schema.Pipe[*schema.Message] that yields the given
// full text split into 3-char chunks. Used to feed a deterministic fake stream
// to drainStream / drain without a real model.
func buildStreamPipe(fullText string) *schema.StreamReader[*schema.Message] {
	sr, sw := schema.Pipe[*schema.Message](4)
	go func() {
		defer sw.Close()
		for i := 0; i < len(fullText); i += 3 {
			end := i + 3
			if end > len(fullText) {
				end = len(fullText)
			}
			sw.Send(&schema.Message{Content: fullText[i:end]}, nil)
		}
	}()
	return sr
}
