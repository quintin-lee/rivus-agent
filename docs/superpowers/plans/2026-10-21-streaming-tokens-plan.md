# Streaming Model Tokens (Follow-up §4) Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the model's live reply stream into the UI in real time (coalesced), instead of waiting for the whole run to finish before the result bubble appears one-shot.

**Architecture:** Backend adds a `model.token` domain event + coalesced emission from `drainStream` (the spot that today swallows the whole stream into one `model.completed`), gated to **parent runs** via an explicit `stream bool` on `drain`. **Zero new plumbing** — `emit` → `OnEvent` → `Events.Append` → SSE `ListAfter` (the same path §3's progress row and approval events already use) automatically persists + replays `model.token`. Frontend accumulates the latest `model.token` per run into a `partialText`, rendered in the running `SystemStatusBubble`; terminal `model.completed` hands off to `AssistantBubble` unchanged. Non-streaming / failure / reconnect all degrade to today's behavior (zero regression).

**Tech Stack:** Go 1.27 + Eino v0.9.21 (`schema.Pipe` for test streams; module `rivus-agent-backend`); React 19 + zustand + Tailwind CSS 4.

**Spec:** `docs/superpowers/specs/2026-10-21-streaming-tokens-design.md` (commit `8d6d596`).

**Key facts this plan grounds against real code:**
- `emit` is `(out chan<- RuntimeEvent, runID string, typ domain.EventType, text, tool, payload string)` — `runner_eino.go:322`. The `model.token` emit is `r.emit(out, runID, domain.EvtModelToken, txt, "", "")` (text in the `text` param, NOT a `map`).
- Persistence bridge: `runtime.go:35-37` marshals `{"text": ev.Text, "tool": ev.ToolName}` → `Events.Append` → `agent_events.payload_json` → SSE. So `model.token` rows land in the store as `payload_json={"text":"...","tool":""}`, and the frontend's `eventPayload(e).text` picks it up. **No new plumbing.**
- `drain` has **4 call sites**: `executeNew` (`runner_eino.go:217`), `executeResume` (`:241`), `RunChild` (`runner_child.go:124`), and the test stub `driveDrain` (`runner_failed_tool_test.go:24`). All must be updated when the signature changes.
- `drainStream` is `runner_eino.go:304-319`; `import "time"` must be added (currently absent).
- Frontend: `partialText` travels `emitRun` → `system-status` `ChatMessage` variant → `ChatMessageList` switch → `SystemStatusBubble` prop (mirrors the existing `progress` field). Component lives at `frontend/src/components/Messages/SystemStatusBubble.tsx` (NOT root-level).
- `AgentEvent.payload_json` is a **string** (`types.ts:52`); the frontend must use the existing `eventPayload(e)` helper (status.ts:119) to parse it, not a `'text' in p` object check.

---

## File Structure

### Backend (5 files)
| File | Responsibility |
|---|---|
| `backend/internal/domain/event.go` | Add `EvtModelToken EventType = "model.token"` to the `EventType` const block (after `EvtModelCompleted`, `:18`). |
| `backend/internal/runtime/runner_eino.go` | `import "time"`; package-level `var tokenFlushInterval = 200 * time.Millisecond`; `drain` gains `stream bool` param; the stream path (`:282-284`) builds an `onDelta` closure + calls `drainStream(mo.MessageStream, onDelta)`; `drainStream` (`:304-319`) gains `onDelta func(textSoFar string)` param + coalescing. |
| `backend/internal/runtime/runner_child.go` | `RunChild`'s `r.drain(...)` call (`:124`): add `false` arg. |
| `backend/internal/runtime/runner_failed_tool_test.go` | `driveDrain` stub's `r.drain(...)` call (`:24`): add `false` arg (4th drain caller). |
| `backend/internal/runtime/runner_stream_test.go` *(new)* | Coalescer unit tests + `drain` gate test (parent `stream=true` vs child `stream=false`). |

### Frontend (6 files)
| File | Responsibility |
|---|---|
| `frontend/src/lib/types.ts` | `EventType` union: add `'model.token'` (after `'model.completed'`, `:32`). |
| `frontend/src/lib/status.ts` | `eventCategory`: `model.token` → `model` bucket (the `model` case, `:57-60`); `eventSummary`: add `model.token` branch (quiet `…streaming`, `:148-151`). |
| `frontend/src/lib/chat-messages.ts` | `system-status` `ChatMessage` variant: add optional `partialText?: string` (`:31`); `emitRun` (`:112-137`): derive `partialText` from the latest `model.token` via `eventPayload`; assign on `statusMsg`. |
| `frontend/src/components/ChatMessageList.tsx` | The `system-status` switch case (`:69-77`): pass `partialText={message.partialText}` to `SystemStatusBubble`. |
| `frontend/src/components/Messages/SystemStatusBubble.tsx` | Add `partialText?: string` prop; render the live partial above the existing `progress` row (`:100-105`), gated on `isActive`. |
| `frontend/src/lib/chat-messages.test.ts` + `frontend/src/components/Messages/SystemStatusBubble.test.tsx` | Tests: `partialText` derivation (active + model.token, terminal, no model.token) + `SystemStatusBubble` renders `partialText` when active. |

---

## Task 1: Backend domain — add `EvtModelToken`

**Files:**
- Modify: `backend/internal/domain/event.go`

- [ ] **Step 1: Add the event type to the const block**

In `backend/internal/domain/event.go`, inside the `const (...)` block (the `EventType` const block at `:6-32`), add one line after `EvtModelCompleted` (`:18`):

```go
const (
	EvtRunCreated        EventType = "run.created"
	...
	EvtModelCompleted    EventType = "model.completed"
	EvtModelToken        EventType = "model.token"   // ← NEW: coalesced full-so-far streaming text (parent runs)
	EvtModelFailed       EventType = "model.failed"
	...
)
```

`model.token` is a `normal`-sensitivity event carrying the **running** assistant full text (full-so-far). `normal` is the default (no `sensitivity` field to set — the persistence bridge in `runtime.go:37` always writes `"normal"`).

- [ ] **Step 2: Build to confirm it compiles**

Run:
```bash
cd backend && go build ./...
```
Expected: PASS (no new usage yet, just a const added to the block).

- [ ] **Step 3: Commit**

```bash
git add backend/internal/domain/event.go
git commit -m "feat(domain): add EvtModelToken event (coalesced streaming full-so-far, parent-run only)"
```

---

## Task 2: Backend drain/drainStream — coalesced streaming + parent gate

**Files:**
- Modify: `backend/internal/runtime/runner_eino.go`
- Modify: `backend/internal/runtime/runner_child.go`
- Modify: `backend/internal/runtime/runner_failed_tool_test.go`

- [ ] **Step 1: Add `import "time"` + package-level `tokenFlushInterval` var**

In `backend/internal/runtime/runner_eino.go`, in the import block (`:3-18`), add `"time"` to the stdlib group (after `"sync"`):

```go
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"   // ← NEW

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	jsonschema "github.com/eino-contrib/jsonschema"
	"rivus-agent-backend/internal/domain"
	itool "rivus-agent-backend/internal/tool"
)
```

Then add the injectable window constant right after the import block (the import `)` closes at `runner_eino.go:18`; insert between the import block and `type RuntimeEvent struct` at `:20`). `import "time"` must already be present from the step above (or add it to the stdlib group if not). Note: there is no existing `lastToolHint`-style var in this file — place it as the first declaration after imports:

```go
// tokenFlushInterval: coalescing window for EvtModelToken emission.
// Each model call produces a small number of EvtModelToken events (one per
// ~200ms window), not one per raw chunk. Injectable in tests (set to a tiny
// value, e.g. 1 * time.Millisecond, to force deterministic flushing).
var tokenFlushInterval = 200 * time.Millisecond
```

**Implementation notes (spec "实现要点", planner must honor):**
- `lastFlush` is the zero-value `time.Time`, so the **first chunk flushes immediately** (`time.Since(zero) >= tokenFlushInterval` is true for any positive interval) — expected, not a bug.
- The coalescer only checks the window **on chunk receipt**; if the upstream stream stalls between chunks, no flush happens during the stall. "~200ms window" is an **upper bound** on inter-flush gaps, not a strict timer.
- **No final flush on stream end** for the last `<200ms` tail. Harmless: `EvtModelCompleted` always carries the full text, and the UI swaps to `AssistantBubble` at terminal. **Do not "fix" this tail** (spec explicitly forbids it).
- `len(sb.String()) > 200000` is O(n) per chunk → O(n²) total, but this is the **existing** check (previously at `runner_eino.go:314`); keep it for fidelity (do not "improve" it — creates an out-of-scope diff).

- [ ] **Step 2: Change `drain` signature + add `stream` param**

In `backend/internal/runtime/runner_eino.go`, change the `drain` function signature (`:246`) to add `stream bool`:

```go
func (r *EinoRunner) drain(ctx context.Context, runID, checkpointID string,
	iter *adk.AsyncIterator[*adk.AgentEvent], out chan<- RuntimeEvent, stream bool) {
```

- [ ] **Step 3: Wire the stream path in `drain` to build `onDelta`**

In the `drain` body, the stream path is at `:282-284` (inside `if ev.Output != nil && ev.Output.MessageOutput != nil`):

Replace:
```go
			if text == "" && mo.MessageStream != nil {
				text = drainStream(mo.MessageStream)
			}
```
With:
```go
			if text == "" && mo.MessageStream != nil {
				var onDelta func(textSoFar string)
				if stream {
					onDelta = func(txt string) { r.emit(out, runID, domain.EvtModelToken, txt, "", "") }
				}
				text = drainStream(mo.MessageStream, onDelta)
			}
```

The `onDelta` closure is built **inside `drain`** (not in `executeNew`/`executeResume`/`RunChild`) — those call sites only pass the `stream bool` (true/false); `drain` itself decides whether to wire the closure. This keeps the 4 call-site changes minimal (just add one bool arg).

- [ ] **Step 4: Change `drainStream` to accept `onDelta` + coalesce**

In `backend/internal/runtime/runner_eino.go`, replace the `drainStream` function (`:304-319`) with:

```go
// drainStream 消费流；onDelta 在每 ~200ms 窗口携带"到目前为止全文"flush 一次。
// 仍返回完整字符串（保证 EvtModelCompleted + lastText 不变）。onDelta 可为 nil。
func drainStream(sr *schema.StreamReader[*schema.Message], onDelta func(textSoFar string)) string {
	defer sr.Close()
	var sb strings.Builder
	var lastFlush time.Time
	for {
		chunk, err := sr.Recv()
		if err != nil {
			break
		}
		if chunk != nil {
			sb.WriteString(chunk.Content)
			if onDelta != nil && time.Since(lastFlush) >= tokenFlushInterval {
				onDelta(sb.String()) // full-so-far
				lastFlush = time.Now()
			}
			if len(sb.String()) > 200000 {
				break
			}
		}
	}
	return sb.String()
}
```

- [ ] **Step 5: Update the 4 drain call sites (add the bool arg)**

4 call sites of `drain` (the spec + this plan enumerate all 4; `go build ./...` will catch any missed):

**(a) `runner_eino.go:217` — `executeNew`** (parent run, streaming → `true`):

```go
		r.drain(ctx, req.RunID, req.CheckpointID, iter, out, true)
```

**(b) `runner_eino.go:241` — `executeResume`** (parent run, streaming → `true`):

```go
		r.drain(ctx, req.RunID, req.CheckpointID, iter, out, true)
```

**(c) `runner_child.go:124` — `RunChild`** (sub-agent, NOT streaming → `false`):

```go
	go func() { defer close(out); r.drain(ctx, req.ChildRunID, req.CheckpointID, iter, out, false) }()
```

**(d) `runner_failed_tool_test.go:24` — `driveDrain` test stub** (test helper, NOT streaming → `false`):

```go
	r.drain(context.Background(), "run-1", "", iter, out, false)
```

- [ ] **Step 6: Build to confirm all call sites compile**

Run:
```bash
cd backend && go build ./...
```
Expected: PASS (all 4 call sites updated; `driveDrain` in the test file compiles with the `false` arg).

- [ ] **Step 7: Run existing runtime tests**

Run:
```bash
cd backend && go test ./internal/runtime/... -v 2>&1 | tail -20
```
Expected: all existing tests PASS (the `driveDrain` stub passes `false`, so `EvtToolFailed` behavior is unchanged; no `EvtModelToken` is emitted in those tests since they use `Ev.Err` paths, not `MessageStream`).

- [ ] **Step 8: Commit**

```bash
git add backend/internal/runtime/runner_eino.go backend/internal/runtime/runner_child.go backend/internal/runtime/runner_failed_tool_test.go
git commit -m "feat(runtime): drainStream coalesced onDelta + drain stream gate (parent true, RunChild/driveDrain false)"
```

---

## Task 3: Backend tests — coalescer + gate

**Files:**
- Create: `backend/internal/runtime/runner_stream_test.go`

- [ ] **Step 1: Write the coalescer test + nil-onDelta test + drain-gate test**

Create `backend/internal/runtime/runner_stream_test.go` (module path is `rivus-agent-backend`, so the `domain` import is `rivus-agent-backend/internal/domain`; the test package is `runtime` — same package as `runner_eino.go`):

```go
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

	// A single AgentEvent carrying a streaming MessageStream (full text "hi there").
	ev := &adk.AgentEvent{
		Output: &adk.AgentOutput{
			MessageOutput: &adk.MessageVariant{
				MessageStream: buildStreamPipe("hi there"),
			},
		},
	}

	gotParent := drainStreamed(t, r, []*adk.AgentEvent{ev}, true)
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

	gotChild := drainStreamed(t, r, []*adk.AgentEvent{ev}, false)
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
```

- [ ] **Step 2: Run the new tests**

Run:
```bash
cd backend && go test ./internal/runtime/ -run 'TestDrainStream' -v
```
Expected: all 3 tests PASS (coalesces + nil-onDelta + gate).

- [ ] **Step 3: Run the full runtime test suite**

Run:
```bash
cd backend && go test ./... 2>&1 | tail -20
```
Expected: all PASS (existing `EvtToolFailed` tests still pass with the `false` arg in `driveDrain`).

- [ ] **Step 4: Commit**

```bash
git add backend/internal/runtime/runner_stream_test.go
git commit -m "test(runtime): drainStream coalescer + drain gate tests (parent streams, sub doesn't)"
```

---

## Task 4: Frontend — types + status

**Files:**
- Modify: `frontend/src/lib/types.ts`
- Modify: `frontend/src/lib/status.ts`

- [ ] **Step 1: Add `model.token` to the `EventType` union**

In `frontend/src/lib/types.ts`, in the `EventType` union (`:19-44`, after `'model.completed'` at `:32`), add:

```ts
export type EventType =
  | 'run.created'
  | 'run.started'
  | 'run.finished'
  | ...
  | 'model.requested'
  | 'model.completed'
  | 'model.token'      // ← NEW: coalesced full-so-far streaming text (parent runs)
  | 'model.failed'
  | ...
```

- [ ] **Step 2: Add `model.token` to `eventCategory` (model bucket)**

In `frontend/src/lib/status.ts`, in the `eventCategory` switch (`:47-77`), add `case 'model.token':` to the model group (lines `57-60`):

```ts
    case 'model.requested':
    case 'model.completed':
    case 'model.token':    // ← NEW
    case 'model.failed':
      return 'model'
```

- [ ] **Step 3: Add `model.token` to `eventSummary` (quiet streaming row)**

In `frontend/src/lib/status.ts`, in the `eventSummary` switch (`:131-186`), add a `model.token` case after `model.completed` (`:148-149`):

```ts
    case 'model.completed':
      return 'model call completed'
    case 'model.token':    // ← NEW: quiet, non-terminal
      return '…streaming'
    case 'model.failed':
      return 'model call failed'
```

- [ ] **Step 4: Type-check**

Run:
```bash
cd frontend && npx tsc --noEmit 2>&1 | head -20
```
Expected: no new errors from the `model.token` addition (existing errors are fine).

- [ ] **Step 5: Commit**

```bash
git add frontend/src/lib/types.ts frontend/src/lib/status.ts
git commit -m "feat(frontend): model.token event type + status category/summary"
```

---

## Task 5: Frontend — emitRun live partial + SystemStatusBubble

**Files:**
- Modify: `frontend/src/lib/chat-messages.ts`
- Modify: `frontend/src/components/ChatMessageList.tsx`
- Modify: `frontend/src/components/Messages/SystemStatusBubble.tsx`

- [ ] **Step 1: Add `partialText?` to the `system-status` ChatMessage variant**

In `frontend/src/lib/chat-messages.ts`, in the `system-status` variant of the `ChatMessage` union (`:21-33`), add an optional `partialText` field:

```ts
  | {
      kind: 'system-status'
      id: string
      runId: string
      run: Run
      status: Run['status']
      mode: string
      goal: string
      steps: Step[]
      budget: Budget
      progress?: { toolCalls: number; modelCalls: number; seconds: number }
      partialText?: string   // ← NEW: live streaming partial from the latest model.token
      error?: { code: string; summary?: string }
    }
```

- [ ] **Step 2: Derive `partialText` in `emitRun` (inside the `isActiveRunStatus` block)**

In `frontend/src/lib/chat-messages.ts`, in `emitRun` (`:96-165`), inside the `if (isActiveRunStatus(run.status))` block (`:113-120`, which already computes `progress`), add the `partialText` derivation alongside `progress`:

```ts
  let progress: { toolCalls: number; modelCalls: number; seconds: number } | undefined
  let partialText: string | undefined   // ← NEW
  if (isActiveRunStatus(run.status)) {
    const evs = eventsByRun[run.id] ?? []
    const toolCalls = evs.filter((e) => e.event_type === 'tool.completed').length
    const modelCalls = evs.filter((e) => e.event_type === 'model.completed').length
    const startMs = run.started_at ?? run.created_at
    const seconds = startMs > 0 ? Math.max(0, Math.round((now - startMs) / 1000)) : 0
    progress = { toolCalls, modelCalls, seconds }

    // NEW: derive live partial from the latest model.token per run.
    // The SSE path (run-store appendEvent) lands ALL model.token rows into
    // eventsByRun (groupEventsByRun buckets by run_id, no event_type filter).
    // payload_json is a string → parse via eventPayload (status.ts:119); the
    // persistence bridge (runtime.go:35-37) stores {"text": "...", "tool": ""}.
    // Take the LAST model.token → its text IS the current partial (full-so-far,
    // idempotent; no diff accumulation needed). On terminal, isActiveRunStatus
    // is false → partialText stays undefined → AssistantBubble takes over.
    const tokens = evs.filter((e) => e.event_type === 'model.token')
    if (tokens.length > 0) {
      const text = eventPayload(tokens[tokens.length - 1]).text
      partialText = typeof text === 'string' ? text : undefined
    }
  }
```

Then add `import { eventPayload }` to the top of `chat-messages.ts` (alongside the existing `import { TERMINAL_RUN_STATUSES, isActiveRunStatus } from './status'`):

```ts
import { TERMINAL_RUN_STATUSES, isActiveRunStatus, eventPayload } from './status'
```

- [ ] **Step 3: Assign `partialText` on `statusMsg`**

In `frontend/src/lib/chat-messages.ts`, the `statusMsg` construction (`:122-136`), add `partialText`:

```ts
  const statusMsg: ChatMessage = {
    kind: 'system-status',
    id: `status-${run.id}`,
    runId: run.id,
    run,
    status: run.status,
    mode: run.mode,
    goal: run.goal,
    steps,
    budget: run.budget,
    error: run.error_code
      ? { code: run.error_code, summary: run.error_summary }
      : undefined,
    progress,
    partialText,   // ← NEW
  }
```

- [ ] **Step 4: Pass `partialText` through `ChatMessageList`**

In `frontend/src/components/ChatMessageList.tsx`, the `system-status` switch case (`:69-77`), add `partialText`:

```tsx
    case 'system-status':
      return (
        <SystemStatusBubble
          run={message.run}
          steps={message.steps}
          progress={message.progress}
          partialText={message.partialText}   // ← NEW
          onRetryRun={onRetry}
        />
      )
```

- [ ] **Step 5: Render `partialText` in `SystemStatusBubble`**

In `frontend/src/components/Messages/SystemStatusBubble.tsx`, add the prop + render. First the `Props` interface (`:8-13`) + the component signature (`:15`):

```tsx
interface Props {
  run: Run
  steps?: Step[]
  onRetryRun: (runId: string) => void
  progress?: { toolCalls: number; modelCalls: number; seconds: number }
  partialText?: string   // ← NEW
}

export function SystemStatusBubble({ run, steps = [], onRetryRun, progress, partialText }: Props) {
```

Then render the partial **above** the existing `progress` row (`:100-105`). The `isActive` variable (`:18`) is already in scope. Insert the new block before the `{isActive && progress && (...)}` row:

```tsx
      <p className="mt-2 text-xs text-foreground/90">{run.goal}</p>
      {isActive && partialText && (
        <p className="mt-1 max-h-40 overflow-hidden text-xs whitespace-pre-wrap text-foreground/80">
          {partialText}
        </p>
      )}
      {isActive && progress && (
        <p className="mt-1 text-xs text-muted-foreground">
          工具 {progress.toolCalls}/{run.budget.max_tool_calls} · 模型 {progress.modelCalls}/{run.budget.max_model_calls} ·{' '}
          {progress.seconds}s
        </p>
      )}
```

**Graceful degradation (spec edge table):**
- `partialText` empty/undefined (no `model.token` yet, or non-streaming provider): the `partialText &&` guard is false → nothing extra renders. The bubble looks exactly as today (progress only).
- Run goes terminal: `isActive` is false → the `isActive && partialText` block is not rendered. The `assistant` `ChatMessage` (emitted at `chat-messages.ts:153-164` for terminal runs) is rendered by `AssistantBubble` instead — the partial disappears, no flicker.

- [ ] **Step 6: Type-check**

Run:
```bash
cd frontend && npx tsc --noEmit 2>&1 | head -20
```
Expected: no new errors (`partialText` is an optional field on the `system-status` variant; `ChatMessageList` passes it; `SystemStatusBubble` accepts it).

- [ ] **Step 7: Commit**

```bash
git add frontend/src/lib/chat-messages.ts frontend/src/components/ChatMessageList.tsx frontend/src/components/Messages/SystemStatusBubble.tsx
git commit -m "feat(frontend): emitRun derives live partialText from model.token; SystemStatusBubble renders it"
```

---

## Task 6: Frontend tests — partialText derivation + rendering

**Files:**
- Modify: `frontend/src/lib/chat-messages.test.ts`
- Modify: `frontend/src/components/Messages/SystemStatusBubble.test.tsx`

- [ ] **Step 1: Add `partialText` derivation tests to `chat-messages.test.ts`**

In `frontend/src/lib/chat-messages.test.ts`, inside the `describe('deriveMessages', ...)` block (add after the existing "does not derive progress for a terminal run" test at `:282-287`), add:

```ts
  it('derives partialText from the latest model.token for an active run', () => {
    const run = mkRun('r1', 'running', { started_at: 1 })
    const evs = [
      mkEv(1, 'tool.completed'),
      mkEv(2, 'model.token', JSON.stringify({ text: 'hel', tool: '' })),
      mkEv(3, 'model.token', JSON.stringify({ text: 'hello', tool: '' })),
    ]
    const out = deriveMessages({ runHistory: [], activeRun: run, activeSteps: [], eventsByRun: { r1: evs }, pendingGoals: [], now: 5001 })
    const status = out.find((m) => m.kind === 'system-status') as Extract<ChatMessage, { kind: 'system-status' }> | undefined
    expect(status?.partialText).toBe('hello')  // latest full-so-far
  })

  it('does not derive partialText for a terminal run (even if model.token rows exist)', () => {
    const run = mkRun('r1', 'succeeded', { started_at: 1 })
    const evs = [
      mkEv(1, 'model.token', JSON.stringify({ text: 'partial', tool: '' })),
      mkEv(2, 'model.completed'),
    ]
    const out = deriveMessages({ runHistory: [run], activeRun: null, activeSteps: [], eventsByRun: { r1: evs }, pendingGoals: [], now: 1000 })
    const status = out.find((m) => m.kind === 'system-status') as Extract<ChatMessage, { kind: 'system-status' }> | undefined
    expect(status?.partialText).toBeUndefined()
  })

  it('derives no partialText when there is no model.token (non-streaming / not yet started)', () => {
    const run = mkRun('r1', 'running', { started_at: 1 })
    const evs = [mkEv(1, 'tool.completed'), mkEv(2, 'model.completed')]
    const out = deriveMessages({ runHistory: [], activeRun: run, activeSteps: [], eventsByRun: { r1: evs }, pendingGoals: [], now: 5001 })
    const status = out.find((m) => m.kind === 'system-status') as Extract<ChatMessage, { kind: 'system-status' }> | undefined
    expect(status?.partialText).toBeUndefined()
  })
```

- [ ] **Step 2: Add a `partialText` rendering test to `SystemStatusBubble.test.tsx`**

In `frontend/src/components/Messages/SystemStatusBubble.test.tsx`, add a test inside the `describe('SystemStatusBubble', ...)` block (add near the existing "renders a progress row" test at `:76-86`):

```tsx
    it('renders a partial-text row only when active + partialText provided', () => {
      // Active run with partialText → row renders.
      let q = render(
        <SystemStatusBubble
          run={run('running')}
          onRetryRun={vi.fn()}
          partialText={'hello wo'}  // mid-stream full-so-far
        />
      )
      expect(screen.getByText('hello wo')).toBeTruthy()
      q.unmount()

      // Terminal run → isActive false → partial row absent even when partialText is passed.
      q = render(
        <SystemStatusBubble
          run={run('succeeded')}
          onRetryRun={vi.fn()}
          partialText={'stale'}
        />
      )
      expect(screen.queryByText('stale')).toBeNull()
      q.unmount()

      // Active run with no partialText → row absent.
      q = render(<SystemStatusBubble run={run('running')} onRetryRun={vi.fn()} />)
      expect(screen.queryByText('hello wo')).toBeNull()
      q.unmount()
    })
```

- [ ] **Step 3: Run the frontend tests**

Run:
```bash
cd frontend && npx vitest run 2>&1 | tail -10
```
Expected: all new + existing tests PASS.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/lib/chat-messages.test.ts frontend/src/components/Messages/SystemStatusBubble.test.tsx
git commit -m "test(frontend): partialText derivation (latest model.token) + SystemStatusBubble rendering"
```

---

## Task 7: Integration — end-to-end smoke + full test suites
**Files:**
- No new files (runs against existing code).

- [ ] **Step 1: Run the full backend test suite**

Run:
```bash
cd backend && go test ./... 2>&1 | tail -20
```
Expected: all PASS.

- [ ] **Step 2: Run the full frontend test suite**

Run:
```bash
cd frontend && npx vitest run 2>&1 | tail -10
```
Expected: all PASS.

- [ ] **Step 3: Build both**

Run:
```bash
cd backend && go build ./... && cd ../frontend && npx tsc --noEmit && npm run build 2>&1 | tail -5
```
Expected: both succeed.

- [ ] **Step 4: Start the dev server and verify manually**

Run (two terminals):
```bash
# 1) backend (needs the model 3-tuple exported; see README "make run"):
cd backend && export MODEL_BASE_URL=... MODEL_API_KEY=... MODEL_NAME=... && make run
# 2) frontend dev server (Vite, proxies to backend):
cd frontend && npm run dev
```

> **If no model is reachable** (the `.env` points at a local Ollama that may have no models pulled): the manual browser step is not runnable. In that case the **integration tests in `backend/tests/integration/` are the authoritative proof** — they exercise the drain → `model.token` → persistence → SSE replay path headlessly. Run those and skip the browser step.

Then in a browser (or `playwright-cli`), open the app, send a chat prompt that triggers a streaming model call, and verify:
- The running bubble (`SystemStatusBubble`) shows a **growing partial text** (the `model.token` full-so-far accumulation, typed out at ~1s granularity via the existing 1s `handleEvents` poll) before the run completes.
- On terminal (`run.finished`/`model.completed`), the partial row **disappears** and the `AssistantBubble` shows the full `result_json` result. No flicker.
- For a non-streaming provider (or a run that only produces `model.completed`, no `model.token`): the running bubble shows the progress row only (same as today) — no partial, **no regression**.
- For a sub-agent run (a `task` tool that spawns a child): the child's output is **not** streamed into the running bubble (the `stream=false` gate in `RunChild` works — sub output stays collapsed into one `tool.completed` row).

**Acceptance criteria:**
- [ ] A streaming model reply streams into the running bubble in real time (≥1 `model.token` event per ~200ms coalesce window per model call; typed out at ~1s granularity by the existing SSE poll).
- [ ] On `model.completed` / run terminal, the bubble transitions to the full `AssistantBubble` with no flicker.
- [ ] Non-streaming / failure / reconnect: zero regression (no `model.token` ⇒ UI is exactly as before this change).
- [ ] Sub-agent (`RunChild`) runs: no `model.token` emitted (the `stream=false` gate works).
- [ ] `go test ./...` all PASS.
- [ ] `npx vitest run` all PASS.

- [ ] **Step 5: Commit (if any files changed during smoke test)**

```bash
git add -A
git commit -m "chore: streaming-tokens end-to-end verification"
```

---

## Rollback

Each task is a separate commit (7 tasks → 7 commits). To roll back a task:
```bash
git revert <commit-sha>
```
Revert in reverse order if a later task broke something. The commits are independent enough that reverting Task 6 (tests) doesn't break Task 5's functionality (tests are additive).

---

## Out of scope (deferred / follow-up, per spec "明确不做")

- **Smooth push channel** (replacing the 1s `handleEvents` ticker with true SSE push): would enable sub-100ms smoothness; a follow-up, not in this plan.
- **Per-sub-run streaming** (`RunChild` → `stream=true`): YAGNI; sub-agent output stays collapsed into one tool result.
- **Cross-run / post-refresh exact token alignment** (depends on backend `budget.consumed`, a §3 follow-up).
- **Partial-token error handling** (truncation on mid-stream failure): `EvtModelFailed` is the terminal; no partial handling needed.
