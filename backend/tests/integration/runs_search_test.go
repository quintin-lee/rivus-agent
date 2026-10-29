package integration

import (
	"strings"
	"testing"

	"rivus-agent-backend/tests/testutil"
)

func TestRunGoalSearch(t *testing.T) {
	h := testutil.New(t)
	ses := h.CreateSession("search-ses")
	payload := fixturePayload(t, ses)
	payload["goal"] = "整理 Alpha 资料"
	if _, code, _ := h.CreateRun(ses, payload, "rs-1"); code != 201 {
		t.Fatalf("create run1 code=%d", code)
	}
	payload2 := fixturePayload(t, ses)
	payload2["goal"] = "beta notes"
	if _, code, _ := h.CreateRun(ses, payload2, "rs-2"); code != 201 {
		t.Fatalf("create run2 code=%d", code)
	}

	code, body := h.Do("GET", "/api/v1/runs?q=alpha", nil, nil)
	if code != 200 {
		t.Fatalf("code=%d body=%s", code, body)
	}
	if !strings.Contains(string(body), "Alpha") || strings.Contains(string(body), "beta notes") {
		t.Fatalf("filter failed: %s", body)
	}

	code, body = h.Do("GET", "/api/v1/runs", nil, nil)
	if code != 200 || !strings.Contains(string(body), "beta notes") {
		t.Fatalf("no-q should return all: %d %s", code, body)
	}
}
