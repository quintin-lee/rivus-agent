package integration

import (
	"testing"
	"time"

	"rivus-agent-backend/tests/testutil"
)

func TestDeleteSessionAPI(t *testing.T) {
	h := testutil.New(t, blockToolDef())
	h.Model.EnqueueToolCall("tc1", "block_tool", `{}`)
	sesBlock := h.CreateSession("blocked")
	if _, code, _ := h.CreateRun(sesBlock, map[string]any{"goal": "slow"}, "del-block"); code != 201 {
		t.Fatalf("create run: %d", code)
	}
	time.Sleep(500 * time.Millisecond)
	if delCode, _ := h.Do("DELETE", "/api/v1/sessions/"+sesBlock, nil, nil); delCode != 409 {
		t.Fatalf("active runs must block delete, got %d", delCode)
	}
	if delCode, _ := h.Do("DELETE", "/api/v1/sessions/"+sesBlock, nil, map[string]string{"X-Owner-ID": "intruder"}); delCode != 404 {
		t.Fatalf("cross-owner must 404, got %d", delCode)
	}

	sesGone := h.CreateSession("bye")
	id, _, _ := h.CreateRun(sesGone, map[string]any{"goal": "hi"}, "del-ok")
	h.WaitTerminal(id, 30*time.Second)
	if delCode, _ := h.Do("DELETE", "/api/v1/sessions/"+sesGone, nil, nil); delCode != 200 {
		t.Fatalf("delete must succeed, got %d", delCode)
	}
	if code, _ := h.Do("GET", "/api/v1/sessions/"+sesGone, nil, nil); code != 404 {
		t.Fatalf("session must be gone, got %d", code)
	}
	if code, _ := h.Do("GET", "/api/v1/runs/"+id, nil, nil); code != 404 {
		t.Fatalf("run must be gone, got %d", code)
	}
	if delCode, _ := h.Do("DELETE", "/api/v1/sessions/"+sesGone, nil, nil); delCode != 404 {
		t.Fatalf("second delete must 404, got %d", delCode)
	}
}
