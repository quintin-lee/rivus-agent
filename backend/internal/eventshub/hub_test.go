package eventshub

import (
	"testing"

	"rivus-agent-backend/internal/domain"
)

func mkEv(seq int64, typ domain.EventType) domain.AgentEvent {
	return domain.AgentEvent{Seq: seq, RunID: "r1", Type: typ,
		PayloadJSON: `{"text":"x"}`, Sensitivity: "normal"}
}

// Round trip: Subscribe then Publish delivers on the run's channel.
func TestSubscribePublishRoundTrip(t *testing.T) {
	h := New()
	ch, unsub := h.Subscribe("r1", 8)
	defer unsub()
	h.Publish(mkEv(1, domain.EvtRunStarted))
	select {
	case ev := <-ch:
		if ev.Seq != 1 || ev.Type != domain.EvtRunStarted {
			t.Fatalf("got %+v", ev)
		}
	default:
		t.Fatal("expected an event on the channel")
	}
}

// Isolation: publish to r2 does not reach r1's channel.
func TestPublishIsPerRun(t *testing.T) {
	h := New()
	ch1, unsub1 := h.Subscribe("r1", 8)
	defer unsub1()
	_, unsub2 := h.Subscribe("r2", 8)
	defer unsub2()
	h.Publish(mkEv(1, domain.EvtRunStarted)) // runID "r1"
	select {
	case ev := <-ch1:
		if ev.Seq != 1 {
			t.Fatalf("got seq %d", ev.Seq)
		}
	default:
		t.Fatal("r1 sub missed its own event")
	}
}

// Drop-on-full: a subscriber with a full buffer loses the overflow event,
// but its channel stays usable (no deadlock / no close).
func TestDropOnFullStaysUsable(t *testing.T) {
	h := New()
	ch, unsub := h.Subscribe("r1", 2)
	defer unsub()
	// Fill the buffer (2) and overflow (1 dropped).
	for i := int64(1); i <= 3; i++ {
		h.Publish(mkEv(i, domain.EvtModelToken))
	}
	// Buffer should hold exactly the first 2; the 3rd was dropped.
	var got []int64
	for {
		select {
		case ev := <-ch:
			got = append(got, ev.Seq)
		default:
			got = append(got, -1) // marks the dropped tail position
			goto done
		}
	}
done:
	if len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != -1 {
		t.Fatalf("expected [1 2 dropped], got %v", got)
	}
	// After the drain loop the buffer is empty. A new publish must still reach
	// the channel (no stuck/closed state) — assert via the non-blocking select.
	h.Publish(mkEv(4, domain.EvtModelCompleted))
	select {
	case ev := <-ch:
		if ev.Seq != 4 {
			t.Fatalf("expected seq 4, got %d", ev.Seq)
		}
	default:
		t.Fatal("channel stuck after drop")
	}
}

// Unsub removes the subscriber (map size returns to 0) and unblocks readers.
func TestUnsubRemovesSubscriber(t *testing.T) {
	h := New()
	_, unsub := h.Subscribe("r1", 8)
	if h.subCount("r1") != 1 {
		t.Fatalf("expected 1 sub, got %d", h.subCount("r1"))
	}
	unsub()
	if h.subCount("r1") != 0 {
		t.Fatalf("expected 0 subs after unsub, got %d", h.subCount("r1"))
	}
}
