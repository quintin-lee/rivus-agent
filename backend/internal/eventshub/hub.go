// Package eventshub is an in-process, per-runID bounded-drop pub/sub fan-out
// for domain.AgentEvent. Publish is non-blocking: each subscriber has a
// buffered channel; when it is full, that subscriber drops the event (the
// consumer's poll backstop replays it). Subscribe returns a read-only channel
// plus an unsubscribe func (prevents subscriber-map leaks).
package eventshub

import (
	"sync"

	"rivus-agent-backend/internal/domain"
)

type Hub struct {
	mu   sync.Mutex
	subs map[string]map[int]chan domain.AgentEvent
	next int
}

func New() *Hub {
	return &Hub{subs: map[string]map[int]chan domain.AgentEvent{}}
}

// Subscribe registers a subscriber for runID with a buffered channel of size
// buf (recommend 64). It returns a read-only channel and an unsubscribe func
// (safe to call once; removes the subscriber from the map).
func (h *Hub) Subscribe(runID string, buf int) (<-chan domain.AgentEvent, func()) {
	if buf < 1 {
		buf = 1
	}
	ch := make(chan domain.AgentEvent, buf)
	h.mu.Lock()
	id := h.next
	h.next++
	if h.subs[runID] == nil {
		h.subs[runID] = map[int]chan domain.AgentEvent{}
	}
	h.subs[runID][id] = ch
	h.mu.Unlock()

	unsub := func() {
		h.mu.Lock()
		if m := h.subs[runID]; m != nil {
			delete(m, id)
			if len(m) == 0 {
				delete(h.subs, runID)
			}
		}
		h.mu.Unlock()
	}
	return ch, unsub
}

// Publish fans out ev to every subscriber of ev.RunID, non-blocking. A
// subscriber whose buffer is full drops ev (its poll backstop catches up).
func (h *Hub) Publish(ev domain.AgentEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ch := range h.subs[ev.RunID] {
		select {
		case ch <- ev:
		default:
			// drop — slow subscriber; backstop (ticker/ListAfter) recovers
		}
	}
}

// subCount is a test-only helper: number of live subscribers for runID.
func (h *Hub) subCount(runID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs[runID])
}
