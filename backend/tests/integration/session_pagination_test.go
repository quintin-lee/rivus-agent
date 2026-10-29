package integration

import (
	"strings"
	"testing"
	"time"

	"rivus-agent-backend/tests/testutil"
)

func TestSessionOffsetPagination(t *testing.T) {
	h := testutil.New(t)
	h.CreateSession("page-a")
	time.Sleep(2 * time.Millisecond) // ms 精度：让 updated_at 可排序
	h.CreateSession("page-b")
	time.Sleep(2 * time.Millisecond)
	h.CreateSession("page-c")

	// updated_at DESC → [page-c, page-b, page-a]；offset=1 → [page-b, page-a]
	code, body := h.Do("GET", "/api/v1/sessions?offset=1", nil, nil)
	if code != 200 {
		t.Fatalf("code=%d body=%s", code, body)
	}
	s := string(body)
	if strings.Contains(s, "page-c") {
		t.Fatalf("offset=1 should exclude newest session: %s", s)
	}
	if !strings.Contains(s, "page-b") || !strings.Contains(s, "page-a") {
		t.Fatalf("offset=1 should keep older sessions: %s", s)
	}

	code, body = h.Do("GET", "/api/v1/sessions?offset=99", nil, nil)
	if code != 200 || !strings.Contains(string(body), `"sessions":[]`) {
		t.Fatalf("beyond-end should be empty: %d %s", code, body)
	}
}

func TestRunsOffsetPagination(t *testing.T) {
	h := testutil.New(t)
	ses := h.CreateSession("pag-ses")
	id1, code, _ := h.CreateRun(ses, fixturePayload(t, ses), "pg-1")
	if code != 201 {
		t.Fatalf("create run1: %d", code)
	}
	time.Sleep(2 * time.Millisecond)
	id2, code, _ := h.CreateRun(ses, fixturePayload(t, ses), "pg-2")
	if code != 201 {
		t.Fatalf("create run2: %d", code)
	}

	// created_at DESC → [run2, run1]；offset=1 跳过最新的 run2
	code, body := h.Do("GET", "/api/v1/runs?session_id="+ses+"&offset=1", nil, nil)
	if code != 200 {
		t.Fatalf("code=%d body=%s", code, body)
	}
	if strings.Contains(string(body), id2) || !strings.Contains(string(body), id1) {
		t.Fatalf("offset=1 should skip newest run %s, keep %s: %s", id2, id1, body)
	}
}
