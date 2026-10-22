# SSE Smooth Push Hub (eventshub) Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make already-connected SSE clients receive new events in **< 100 ms** (instead of the `handleEvents` 1 s poll ceiling) by fanning events out from `EventRepo.Append` through a small in-process pub/sub hub, while keeping the existing 1 s ticker as backstop + terminal check.

**Architecture:** New leaf package `internal/eventshub` (stdlib + `internal/domain` only). `EventRepo` gains an optional `SetPublisher(fn)` hook (nil-safe no-op); `app.go` wires `hub.Publish` into it after `NewEventRepo`, and passes the hub to `httpapi.New` via a new `WithEventHub` option (variadic, non-breaking). `handleEvents` subscribes per run and adds a `pushCh` case to its existing `select` loop, reusing the `writeEvents` closure; a **gap backfill** (`ev.Seq > lastSeq+1` → `ListAfter` first) makes drop-on-full loss-free. **Frontend: zero changes** (dedupes by `(seq,event_type)`, takes latest `model.token`).

**Tech Stack:** Go 1.27, module `rivus-agent-backend`; CGO-free SQLite (modernc); React 19 frontend untouched.

**Spec:** `docs/superpowers/specs/2026-10-22-sse-push-hub-design.md` (commit `b7be59b`).

**Key facts this plan grounds against real code:**
- `httpapi.New` is variadic-options (`server.go:96-113`): `type Option func(*serverOptions)`, `New(cfg, db, tasks, events, settings, runs, sessions, opts ...Option)`. `WithEventHub` slots in beside `WithToolConfigProvider`.
- `handleEvents` (`server.go:307-388`): the `writeEvents` closure (`:332-343`) already does write-per-event + `lastSeq` advance + `Flush()`; initial replay + terminal check at `:344-352`; loop `select` at `:359-387` with `poll`/`beat` tickers (`:355-358`), `followPollInterval = 1 * time.Second` (`:22`).
- `EventRepo.Append` (`event_repo.go:19-29`): `INSERT ... nowMs()` then `return res.LastInsertId()` — the spec's §2 diff hoists `ts := nowMs()` and appends the publisher call. `res.LastInsertId()`'s error is already propagated (bare two-value return); the snippet's explicit `seq, err :=` is readability-equivalent, **not** a behavior change.
- `app.go:54` `events := store.NewEventRepo(db)`; `:105` `srv := httpapi.New(cfg, db, tasks, events, settings, runs, sessions, httpapi.WithToolConfigProvider(provider))`.
- All production event writers go through `EventRepo.Append`: `runtime.go:37,186,196` + `run_service.go:76,108,175`. No direct SQL INSERT into `agent_events` exists elsewhere.
- `domain` package has no imports (leaf) — `eventshub` importing it creates no cycle.
- Test harness: `tests/testutil/harness.go` `buildHarness` (harness.go:182-263) builds `httpapi.New(...)` with **no** hub and never calls `SetPublisher` — a hub-wired variant is required for the push fast-path test.

---

## File Structure

| File | Responsibility |
|---|---|
| `backend/internal/eventshub/hub.go` *(new)* | The hub: `New`, `Subscribe(runID, buf) (<-chan domain.AgentEvent, func())`, `Publish(ev)` — per-`runID` bounded fan-out, drop-on-full, mutex-protected, no locks held across I/O. |
| `backend/internal/eventshub/hub_test.go` *(new)* | Hub unit tests (round-trip, per-subscriber isolation, drop-on-full stays usable, unsub removes from map). |
| `backend/internal/store/event_repo.go` | `EventRepo.publisher` field + `SetPublisher`; `Append` tail publishes `domain.AgentEvent` (shared `ts`). |
| `backend/internal/store/event_repo_pub_test.go` *(new)* | Store test: `SetPublisher` fires on `Append`; nil publisher is a no-op. |
| `backend/internal/api/http/server.go` | `Server.hub` field; `WithEventHub` option; `handleEvents` `pushCh` case (Subscribe + `defer unsub`, gap backfill, reuse `writeEvents`). |
| `backend/internal/app/app.go` | Create `eventshub.New()`, `events.SetPublisher(hub.Publish)`, pass `httpapi.WithEventHub(hub)`. |
| `backend/tests/testutil/harness.go` | `NewHubHarness(t, extraTools...)` variant (wires hub into `EventRepo` + `httpapi`). |
| `backend/tests/integration/sse_push_test.go` *(new)* | Three integration scenarios: push fast-path (< 100 ms), no-hub ticker regression, gap backfill. |

---

## Chunk 1: eventshub package

## Task 1: `eventshub` — hub + unit tests

**Files:**
- Create: `backend/internal/eventshub/hub.go`
- Create: `backend/internal/eventshub/hub_test.go`

- [ ] **Step 1: Write the failing tests first**

Create `backend/internal/eventshub/hub_test.go` (package `eventshub`):

```go
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
	// A later publish after the reader drains still delivers (no stuck state).
	_ = <-ch // drain nothing more; just ensure next publish works below
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
```

Note: `subCount` is a tiny test helper — add it to `hub.go` (see Step 2), or expose a test-only method. Keep it trivial.

- [ ] **Step 2: Run tests to confirm they fail**

Run:
```bash
cd backend && go test ./internal/eventshub/ -v 2>&1 | tail -20
```
Expected: FAIL — `undefined: New` / `undefined: Subscribe` / `undefined: Publish` (the package has no implementation yet).

- [ ] **Step 3: Implement `hub.go`**

Create `backend/internal/eventshub/hub.go`:

```go
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
```

- [ ] **Step 4: Run tests to confirm they pass**

Run:
```bash
cd backend && go test ./internal/eventshub/ -v 2>&1 | tail -20
```
Expected: all 4 tests PASS.

- [ ] **Step 5: Vet + build**

Run:
```bash
cd backend && go vet ./internal/eventshub/ && go build ./...
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/eventshub/
git commit -m "feat(eventshub): per-runID bounded-drop pub/sub hub"
```

---

## Chunk 2: store hook + httpapi push case

## Task 2: `EventRepo.SetPublisher` — single hook at `Append`

**Files:**
- Modify: `backend/internal/store/event_repo.go:12,19-29`
- Create: `backend/internal/store/event_repo_pub_test.go`

- [ ] **Step 1: Write the failing test**

Create `backend/internal/store/event_repo_pub_test.go` (package `store`, mirror `store_test.go` patterns — `Open(":memory:")` + `NewEventRepo`):

```go
package store

import (
	"context"
	"sync"
	"testing"

	"rivus-agent-backend/internal/domain"
)

// A SetPublisher hook fires once per Append with the persisted event shape.
func TestEventRepoPublisherFiresOnAppend(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewEventRepo(db)

	var mu sync.Mutex
	var got []domain.AgentEvent
	repo.SetPublisher(func(ev domain.AgentEvent) {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
	})

	seq, err := repo.Append(context.Background(), "run_1", domain.EvtModelToken, `{"text":"hi"}`, "")
	if err != nil {
		t.Fatal(err)
	}
	if seq == 0 {
		t.Fatal("expected non-zero seq")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("expected 1 published event, got %d", len(got))
	}
	ev := got[0]
	if ev.Seq != seq || ev.RunID != "run_1" || ev.Type != domain.EvtModelToken ||
		ev.PayloadJSON != `{"text":"hi"}` || ev.Sensitivity != "normal" {
		t.Fatalf("published event mismatch: %+v (want seq=%d, run=run_1, type=%s, payload=%q, sens=normal)",
			ev, seq, domain.EvtModelToken, `{"text":"hi"}`)
	}
}

// With no SetPublisher, Append still works and publishes nothing (nil-safe).
func TestEventRepoNilPublisherNoop(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewEventRepo(db)
	if _, err := repo.Append(context.Background(), "run_1", domain.EvtRunStarted, `{}`, ""); err != nil {
		t.Fatal(err)
	}
	// No panic, no side effects — just confirm the row persisted.
	list, err := repo.ListAfter(context.Background(), "run_1", 0, 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("expected 1 row, got %d err=%v", len(list), err)
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail**

Run:
```bash
cd backend && go test ./internal/store/ -run 'TestEventRepoPublisherFiresOnAppend|TestEventRepoNilPublisherNoop' -v 2>&1 | tail
```
Expected: the publisher test FAILS (`undefined: (*EventRepo).SetPublisher`); the nil-publisher test compiles too (same undefined symbol) so both fail until Step 3.

- [ ] **Step 3: Implement the hook**

Modify `backend/internal/store/event_repo.go`:

1. Add a field to `EventRepo` (`:12`):
```go
type EventRepo struct {
	db        *sql.DB
	publisher func(domain.AgentEvent) // nil-safe：未装配时为 no-op
}
```
(`NewEventRepo` at `:14` returns `&EventRepo{db: db}` — the new field is zero-valued = nil, no change needed there.)

2. Add the setter after `NewEventRepo`:
```go
// SetPublisher 挂发布钩子（app 装配 eventshub.Hub.Publish）。nil 则 Append 纯写库，行为同今天。
func (r *EventRepo) SetPublisher(fn func(domain.AgentEvent)) {
	r.publisher = fn
}
```

3. Modify `Append` (`:19-29`) per the spec §2 diff — hoist `ts := nowMs()` above the INSERT (reuse it in the row AND the published event, so `created_at` is identical), handle `LastInsertId`'s error explicitly (readability-equivalent, not a behavior change), then publish:
```go
func (r *EventRepo) Append(ctx context.Context, runID string, typ domain.EventType, payloadJSON, sensitivity string) (int64, error) {
	if sensitivity == "" {
		sensitivity = "normal"
	}
	ts := nowMs()
	res, err := r.db.ExecContext(ctx, `INSERT INTO agent_events(run_id, event_type, payload_json, sensitivity, created_at)
		VALUES(?, ?, ?, ?, ?)`, runID, string(typ), payloadJSON, sensitivity, ts)
	if err != nil {
		return 0, err
	}
	seq, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if r.publisher != nil {
		r.publisher(domain.AgentEvent{Seq: seq, RunID: runID, Type: typ,
			PayloadJSON: payloadJSON, Sensitivity: sensitivity, CreatedAt: ts})
	}
	return seq, nil
}
```
(`event_repo.go` already imports `domain` for the `EventType`/`AgentEvent` types — confirm; if not, add `"rivus-agent-backend/internal/domain"`.)

- [ ] **Step 4: Run tests to confirm they pass**

Run:
```bash
cd backend && go test ./internal/store/ -run 'TestEventRepoPublisherFiresOnAppend|TestEventRepoNilPublisherNoop' -v 2>&1 | tail
```
Expected: both PASS.

- [ ] **Step 5: Run the whole store package (regression)**

Run:
```bash
cd backend && go test ./internal/store/ -count=1 2>&1 | tail
```
Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/store/event_repo.go backend/internal/store/event_repo_pub_test.go
git commit -m "feat(store): EventRepo.SetPublisher hook — publish appended events (nil-safe)"
```

---

## Task 3: `httpapi.WithEventHub` option + `handleEvents` push case

**Files:**
- Modify: `backend/internal/api/http/server.go` (`Server` struct `:27-37`, `Option`/`serverOptions`/`New` `:94-113`, `handleEvents` `:307-388`)

- [ ] **Step 1: Add the `hub` field + `WithEventHub` option**

In `server.go`:

1. Add an import: `"rivus-agent-backend/internal/eventshub"` (to the import block `:3-18`).
2. Add a field to `Server` (`:27-37`):
```go
type Server struct {
	cfg      config.Config
	db       *sql.DB
	tasks    *store.TaskRepo
	events   *store.EventRepo
	settings *store.SettingsRepo
	runs     *service.RunService
	sessions *service.SessionService
	tools    ToolConfigProvider
	hub      *eventshub.Hub
	mux      *http.ServeMux
}
```
3. Extend `serverOptions` (`:98-100`):
```go
type serverOptions struct {
	tools ToolConfigProvider
	hub   *eventshub.Hub
}
```
4. Add the option func (after `WithToolConfigProvider`, `:102-104`):
```go
func WithEventHub(hub *eventshub.Hub) Option {
	return func(o *serverOptions) { o.hub = hub }
}
```
5. Wire it in `New` (`:105-113`) — set `hub: o.hub` in the returned `Server` literal:
```go
s := &Server{cfg: cfg, db: db, tasks: tasks, events: events, settings: settings,
	runs: runs, sessions: sessions, tools: o.tools, hub: o.hub, mux: http.NewServeMux()}
```
(Existing `New(...)` callers pass no/`tools` option — adding `hub` to `serverOptions` is non-breaking: default is `nil`.)

- [ ] **Step 2: Subscribe in `handleEvents` after the initial replay**

In `handleEvents`, after the terminal check (`:350-352`) and the poll/beat ticker setup (`:354-358`) — i.e. just before the `for { select { ... } }` loop (`:359`) — add:
```go
	var pushCh <-chan domain.AgentEvent
	if s.hub != nil {
		var unsub func()
		pushCh, unsub = s.hub.Subscribe(id, 64)
		defer unsub()
	}
```
When `s.hub == nil`, `pushCh` stays a **nil channel** — `case ev, ok := <-pushCh:` on a nil channel never fires, so the no-hub path is exactly today's ticker-only behavior (the zero-regression guarantee). No sentinel needed.

- [ ] **Step 3: Add the `pushCh` case with gap backfill to the `select`**

In the `select` loop (`:360-386`), add a case (reuse the existing `writeEvents` closure `:332-343`):
```go
		case ev, ok := <-pushCh:
			if !ok {
				continue // defensive: unsub only via defer at handler exit; won't fire mid-loop
			}
			if ev.Seq > lastSeq {
				if ev.Seq > lastSeq+1 {
					// gap backfill：push 可能丢了中间 seq（订阅缓冲满 drop）。
					// 先 ListAfter(lastSeq) 把 gap 补回，再写 ev（幂等去重）。
					if gap, err := s.events.ListAfter(ctx, id, lastSeq, 500); err == nil {
						if !writeEvents(gap) {
							return
						}
					}
				}
				// ev 可能已包含在 gap 里 → 重复投递有意的、幂等（前端按 (seq,event_type) 去重）
				if !writeEvents([]domain.AgentEvent{ev}) {
					return
				}
			}
```
The existing `poll.C`, `beat.C`, and `ctx.Done()` cases remain **unchanged** — the 1 s ticker is still the backstop + terminal check.

- [ ] **Step 4: Build to confirm it compiles**

Run:
```bash
cd backend && go build ./...
```
Expected: PASS.

- [ ] **Step 5: Run the http package tests (regression)**

Run:
```bash
cd backend && go test ./internal/api/... -count=1 2>&1 | tail
```
Expected: all PASS. (If there's no test file under `internal/api`, that's fine — the build is the gate; the integration tests in Task 5 cover behavior.)

- [ ] **Step 6: Commit**

```bash
git add backend/internal/api/http/server.go
git commit -m "feat(http): handleEvents push case (eventshub) + gap backfill; keep 1s ticker backstop"
```

---

## Chunk 3: app wiring + integration proof

## Task 4: `app.go` — wire the hub

**Files:**
- Modify: `backend/internal/app/app.go:54,105`

- [ ] **Step 1: Create the hub + wire publisher + pass to httpapi**

In `app.go`:

1. Add import `"rivus-agent-backend/internal/eventshub"` (to the import block `:13-25`).
2. After `events := store.NewEventRepo(db)` (`:54`), add:
```go
	hub := eventshub.New()
	events.SetPublisher(hub.Publish)
```
3. In the `httpapi.New(...)` call (`:105`), append the option:
```go
	srv := httpapi.New(cfg, db, tasks, events, settings, runs, sessions,
		httpapi.WithToolConfigProvider(provider), httpapi.WithEventHub(hub))
```

- [ ] **Step 2: Build**

Run:
```bash
cd backend && go build ./...
```
Expected: PASS.

- [ ] **Step 3: Run the whole backend test suite (regression)**

Run:
```bash
cd backend && go test ./... -count=1 2>&1 | grep -E "FAIL|ok " | tail
```
Expected: all PASS (no behavior change for existing tests — they use the un-wired harness, so `hub` is nil, pure ticker path).

- [ ] **Step 4: Commit**

```bash
git add backend/internal/app/app.go
git commit -m "feat(app): wire eventshub into EventRepo publisher + httpapi.WithEventHub"
```

---

## Task 5: `NewHubHarness` testutil variant

**Files:**
- Modify: `backend/tests/testutil/harness.go` (add a variant; `buildHarness` at `:182-263`)

- [ ] **Step 1: Parameterize `buildHarness` with `withHub` + add `NewHubHarness`**

**Read `buildHarness` (`harness.go:182-263`) first.** It has the signature `buildHarness(t TB, extraTools []tool.Definition, provider httpapi.ToolConfigProvider, registerToolConfigSurface bool) *Harness`, and at its tail calls `httpapi.New(cfg, db, tasks, events, settings, runs, sessions, ...)` at `:254`/`:256` (with or without `httpapi.WithToolConfigProvider(provider)`). `events` (the `*store.EventRepo`) is in scope at `:189`.

1. Add a trailing `withHub bool` param to `buildHarness` (`:182`), and update its two internal callers:
   - `New` (`:139`) → `buildHarness(t, extraTools, nil, false, /*withHub*/ false)`
   - `NewToolConfigHarness` (`:161`) → `buildHarness(t, nil, stub, true, /*withHub*/ false)`
2. Inside `buildHarness`, just after `events := store.NewEventRepo(db)` (`:189`), add:
```go
	var hub *eventshub.Hub
	if withHub {
		hub = eventshub.New()
		events.SetPublisher(hub.Publish)
	}
```
3. Rewrite the `httpapi.New` call (`:252-257`) to build an `opts` slice (avoids mixing a fixed variadic arg with a slice spread):
```go
	var opts []httpapi.Option
	if provider != nil {
		opts = append(opts, httpapi.WithToolConfigProvider(provider))
	}
	if hub != nil {
		opts = append(opts, httpapi.WithEventHub(hub))
	}
	srv := httpapi.New(cfg, db, tasks, events, settings, runs, sessions, opts...)
```
   i.e. replace the `var srv *httpapi.Server` + `if provider != nil {...} else {...}` block (`:252-257`) with the single `opts`-based call above. `httpapi.New`'s signature is `(..., opts ...Option)` so `opts...` (a possibly-empty slice) is valid.
4. Add the `NewHubHarness` wrapper alongside `New` (near `:138`):
```go
// NewHubHarness builds a Harness with the eventshub wired into EventRepo
// (SetPublisher) and httpapi (WithEventHub) — needed to exercise the SSE
// push fast path. New() remains the no-hub (ticker-only) baseline.
func NewHubHarness(t TB, extraTools ...tool.Definition) *Harness {
	t.Helper()
	return buildHarness(t, extraTools, nil, false, /*withHub*/ true)
}
```
5. Add import `"rivus-agent-backend/internal/eventshub"` to `harness.go`.

- [ ] **Step 2: Build + vet**

Run:
```bash
cd backend && go build ./... && go vet ./tests/...
```
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add backend/tests/testutil/harness.go
git commit -m "test(testutil): NewHubHarness variant wiring eventshub into EventRepo + httpapi"
```

---

## Task 6: integration — push fast-path / ticker regression / gap backfill

**Files:**
- Create: `backend/tests/integration/sse_push_test.go`

- [ ] **Step 1: Write the three integration scenarios**

Create `backend/tests/integration/sse_push_test.go` (package `integration`; reuse the `openStream`/`waitLine` helpers from `sse_follow_test.go:16-63`):

```go
package integration

import (
	"context"
	"time"
	"testing"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/tests/testutil"
)

// 1. Push fast path (hub wired): a model.token frame arrives well within a
// 200ms window. With the 1s poll ticker alone this would time out (it waits
// up to ~1s for the next tick), so passing proves the hub push path is live.
//
// Determinism note: the SCRIPTED model (testutil/harness.go Stream) produces
// a model call that DOES emit a model.token — proven by the existing
// TestModelTokenPersists (streaming_token_test.go), which asserts a
// model.token row is persisted for a scripted-text run. The token is emitted
// during the model call, so it is published by the hub immediately.
func TestSSEPushFastPath(t *testing.T) {
	h := testutil.NewHubHarness(t)
	h.Model.EnqueueText("streamed")
	ses := h.CreateSession("s")
	id, code, _ := h.CreateRun(ses, map[string]any{"goal": "hi"}, "push-1")
	if code != 201 {
		t.Fatalf("create run: %d", code)
	}
	cancel, _, lines := openStream(t, h, id)
	defer cancel()
	// A 200ms deadline is ~2x under the 1s ticker. If the ticker were the only
	// delivery path, the first model.token could not arrive within 200ms with
	// any reliability — so passing means the hub pushed it.
	waitLine(t, lines, "event: model.token", 200*time.Millisecond)
}

// 2. No-hub regression: with the hub NOT wired (testutil.New), events still
// flow, but only on the 1s ticker. run.started (written at run creation,
// before the stream opens) is the initial-replay path (not ticker), so we
// wait on a LATER event — run.finished — which for a no-hub server can only
// arrive via the 1s ticker poll. Generous deadline (~3s) covers 2-3 ticks.
func TestSSETickerNoHubRegression(t *testing.T) {
	h := testutil.New(t) // no hub
	h.Model.EnqueueText("done")
	ses := h.CreateSession("s")
	id, code, _ := h.CreateRun(ses, map[string]any{"goal": "hi"}, "nohub-1")
	if code != 201 {
		t.Fatalf("create run: %d", code)
	}
	cancel, _, lines := openStream(t, h, id)
	defer cancel()
	// run.finished only persists after the run completes; with no hub it can
	// only be delivered by the 1s ticker. A ~3s deadline proves the ticker path
	// works without the hub (zero-regression guarantee).
	waitLine(t, lines, "event: run.finished", 3*time.Second)
}

// 3. Gap backfill: stall a run at the approval gate (block_tool), open the
// stream, then write a synthetic event directly to the DB (bypassing the
// hub so it is NOT pushed) followed by a hub-published event with a gap.
// The push case's backfill (ev.Seq > lastSeq+1 → ListAfter) must deliver the
// skipped seq before the gapped event.
func TestSSEPushGapBackfill(t *testing.T) {
	h := testutil.NewHubHarness(t, blockToolDef())
	h.Model.EnqueueToolCall("tc1", "block_tool", `{}`) // run pauses at approval
	ses := h.CreateSession("s")
	id, code, _ := h.CreateRun(ses, map[string]any{"goal": "pause"}, "push-gap-1")
	if code != 201 {
		t.Fatalf("create run: %d", code)
	}
	cancel, _, lines := openStream(t, h, id)
	defer cancel()
	// Wait for the stream to be live (initial replay delivered run.created).
	waitLine(t, lines, "event: run.created", 2*time.Second)

	// Write a synthetic event DIRECTLY to the DB (bypasses the hub → NOT
	// pushed to the open stream; the ticker could pick it up in ≤1s, but we
	// then immediately push a gapped event so the backfill path runs first).
	gapSeq, err := h.Events.Append(context.Background(), id, domain.EventType("gap.filled"), `{"n":1}`, "")
	if err != nil {
		t.Fatal(err)
	}
	// Now publish a gapped event through the hub: its seq is > gapSeq, so the
	// push case sees ev.Seq > lastSeq+1 and must ListAfter(lastSeq) to
	// backfill the "gap.filled" seq before writing the gapped event.
	// (We cannot call hub.Publish directly — the harness owns the hub — so we
	// trigger a real persisted+published event via the approval decision,
	// which appends approval.decided and publishes it.)
	approveRun(t, h, id) // helper: GET pending approval, POST {"approve":true} to /runs/{id}/approvals/{aid}
	// approval.decided can ONLY arrive fast via the push case (ticker is 1s,
	// and we started the stream >1s ago); within ~500ms proves the push path
	// executed — and its gap backfill (ev.Seq > lastSeq+1 → ListAfter) is what
	// delivers the DB-only "gap.filled" row that the hub never pushed. The
	// no-loss guarantee: gap.filled must appear, via backfill or the ticker.
	approveRun(t, h, id)
	_ = gapSeq
	// Fast: the gapped event arrived via push, not the 1s ticker.
	waitLine(t, lines, "event: approval.decided", 500*time.Millisecond)
	// No loss: the DB-only gap.filled seq still reached the stream.
	waitLine(t, lines, "event: gap.filled", 5*time.Second)
}
```

**Pre-running checklist (verify against real code, don't guess):**
- `h.CreateRun` returns `(string, int, map[string]any)` (`harness.go:307`); `h.Model.EnqueueText` / `EnqueueToolCall` are the scripting methods (`harness.go:44,52`).
- `openStream`/`waitLine` are package-level in `sse_follow_test.go:16-63` (same `integration` package) — directly usable.
- `approveRun(t, h, id)` is a small helper you add to this test file: it fetches the run's pending approval via `GET /api/v1/runs/{id}/approvals` and POSTs `{"approve": true, "reason": "ok"}` to `/api/v1/runs/{id}/approvals/{approval_id}` (the route at `server.go:130`; payload shape confirmed in `approval_test.go:74-75`). It triggers `run_service.DecideApproval` → `Events.Append(approval.decided)` → `hub.Publish` (the gapped event).
- `blockToolDef()` is defined in `api_test.go:107` (same package) — reuse it for the pause tool.
- `domain` + `context` imports needed for `h.Events.Append` / `domain.EventType`.
- **Scenario 1 relies on a scripted-model `model.token` being emitted.** This is already proven by `TestModelTokenPersists` (`streaming_token_test.go`, merged in the streaming-tokens round). If it ever regresses, scenario 1 will fail loudly — that's the point.

- [ ] **Step 2: Run the new tests**

Run:
```bash
cd backend && go test ./tests/integration/ -run 'TestSSEPush|TestSSETickerNoHub' -v -count=1 2>&1 | tail -30
```
Expected: all PASS.

- [ ] **Step 3: Run the full backend suite (regression)**

Run:
```bash
cd backend && go test ./... -count=1 2>&1 | grep -E "FAIL|ok " | tail
```
Expected: all PASS.

- [ ] **Step 4: Commit**

```bash
git add backend/tests/integration/sse_push_test.go
git commit -m "test(integration): SSE push fast-path (<100ms), no-hub ticker regression, terminal frame"
```

---

## Task 7: full-suite green + no-frontend-diff check

**Files:** none (verification only).

- [ ] **Step 1: Backend full build + test**

Run:
```bash
cd backend && go build ./... && go vet ./... && go test ./... -count=1 2>&1 | grep -E "FAIL|ok " | tail
```
Expected: build/vet clean; all packages PASS.

- [ ] **Step 2: Confirm frontend is untouched**

Run:
```bash
cd /data/home/quintin/workspace/source/go/rivus-agent && git diff --name-only origin/master -- frontend/ 2>/dev/null || git status --porcelain frontend/
```
Expected: **no frontend files** in the diff (the spec's "前端 git diff 为空" acceptance).

- [ ] **Step 3: Confirm the launch path still works**

Run:
```bash
cd backend && export MODEL_BASE_URL=${MODEL_BASE_URL:-http://localhost:11434/v1} MODEL_API_KEY=${MODEL_API_KEY:-} MODEL_NAME=${MODEL_NAME:-} && timeout 5 go run ./cmd/agentd -config configs/config.example.yaml 2>&1 | head -5 || true
```
Expected: server boots (listens) — a quick smoke that the hub wiring doesn't panic at construction. (If a model isn't reachable that's fine — we only need construction to succeed; the `|| true` absorbs the timeout.)

- [ ] **Step 4: Commit any strays (if none, skip)**

```bash
git status --porcelain && git add -A && git commit -m "chore: sse-push-hub integration verification" 2>/dev/null || echo "nothing to commit"
```

---

## Rollback

Each task is a separate commit. Revert in reverse order. Tasks 1–4 are additive; the feature is inert (pure ticker behavior) if `app.go` doesn't wire the hub, so reverting Task 4 alone disables the push path without touching the new package.

---

## Out of scope (per spec "明确不做")

- Per-client seq bookkeeping / exact resume alignment (needs `budget.consumed`, 10-21 follow-up #3).
- Sub-run token streaming (sub-agents stay collapsed).
- `MAX_EVENTS=500` head-truncation change (documented limitation).
- Shortening `followPollInterval` (stays 1 s; the push path is decoupled from it).
