package integration

import (
	"testing"
	"time"

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
// flow. run.finished is delivered either via the INITIAL REPLAY path
// (server.go:344 ListAfter(0), if the fast scripted run already terminated
// before openStream) or the 1s ticker (if it terminates after) — either way
// the test proves events flow WITHOUT a hub (zero-regression guarantee).
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
	// With no hub, run.finished is delivered by the initial replay or the 1s
	// ticker (path-independent) — a ~3s deadline covers both. The ticker-only
	// path is additionally proven by the existing TestSSEFollowPushHeartbeatClose.
	waitLine(t, lines, "event: run.finished", 3*time.Second)
}

// 3. Gap backfill: stall a run at the approval gate (publishToolDef), open the
// stream, then write a synthetic event directly to the DB (bypassing the
// hub so it is NOT pushed) followed by a hub-published event with a gap.
// The push case's backfill (ev.Seq > lastSeq+1 → ListAfter) must deliver a
// skipped seq that was written to the DB but NOT pushed through the hub.
//
// Fixture: publishToolDef (ApprovalAlways) stalls the run at the approval
// gate, so the stream stays open long enough to set up the gap.
func TestSSEPushGapBackfill(t *testing.T) {
	h := testutil.NewHubHarness(t, publishToolDef())
	h.Model.EnqueueToolCall("tc1", "publish_draft", `{"title":"hi"}`) // run stalls at approval
	ses := h.CreateSession("s")
	id, code, _ := h.CreateRun(ses, map[string]any{"goal": "pause"}, "push-gap-1")
	if code != 201 {
		t.Fatalf("create run: %d", code)
	}
	// Wait until the run is parked at the approval gate (gives the stream time
	// to come up AND advances lastSeq past the pre-approval events).
	// waitApproval (approval_test.go:30, same package) returns the pending record.
	a := waitApproval(t, h, id)

	cancel, _, lines := openStream(t, h, id)
	defer cancel()
	waitLine(t, lines, "event: run.created", 2*time.Second) // initial replay: stream live

	// 1) Write a "gap" event DIRECTLY to the DB, bypassing the hub (no
	//    EventRepo.Append, so SetPublisher never fires → NOT pushed). This
	//    creates a seq that no push will carry.
	if _, err := h.DB.Exec(
		`INSERT INTO agent_events(run_id, event_type, payload_json, sensitivity, created_at)
			VALUES(?, 'gap.filled', '{"n":1}', 'normal', ?)`,
		id, time.Now().UnixMilli(), // mirrors store.nowMs (task_repo.go:17)
	); err != nil {
		t.Fatal(err)
	}

	// 2) Now approve → run_service.DecideApproval → Events.Append(EvtApprovalDecided)
	//    → hub.Publish. That published seq is > the gap row's seq, so the push
	//    case sees ev.Seq > lastSeq+1 and MUST ListAfter(lastSeq) to backfill.
	if code, body := h.Do("POST", "/api/v1/runs/"+id+"/approvals/"+a.ID,
		map[string]any{"approve": true, "reason": "ok"}, nil); code != 200 {
		t.Fatalf("decide approval: %d %s", code, body)
	}

	// 3) The gapped batch arrives FAST via the push path's backfill: the
	// server writes the ListAfter gap batch in ascending seq order
	// (gap.filled first, then approval.decided) followed by the duplicate
	// pushed event (server.go:386-400). A 1s ticker cannot deliver either
	// that quickly. Assert both frames: gap.filled (DB-only, backfilled)
	// AND the pushed approval.decided (proving no loss).
	waitLine(t, lines, "event: gap.filled", 500*time.Millisecond)
	waitLine(t, lines, "event: approval.decided", 5*time.Second)
}
