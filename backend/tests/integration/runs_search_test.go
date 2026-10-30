package integration

import (
	"context"
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

func TestRunResultSearch(t *testing.T) {
	h := testutil.New(t)
	ses := h.CreateSession("result-search-ses")
	payload := fixturePayload(t, ses)
	payload["goal"] = "gamma task"
	id, code, _ := h.CreateRun(ses, payload, "rs-3")
	if code != 201 {
		t.Fatalf("create run code=%d", code)
	}
	if err := h.Tasks.SetResult(context.Background(), id, `{"summary":"RESULT-MARKER-X 报告"}`); err != nil {
		t.Fatalf("set result: %v", err)
	}

	code, body := h.Do("GET", "/api/v1/runs?q=result-marker-x", nil, nil)
	if code != 200 {
		t.Fatalf("code=%d body=%s", code, body)
	}
	if !strings.Contains(string(body), "RESULT-MARKER-X") {
		t.Fatalf("result_json hit expected: %s", body)
	}
	code, body = h.Do("GET", "/api/v1/runs?q=zzz-not-there", nil, nil)
	if code != 200 || strings.Contains(string(body), "gamma task") {
		t.Fatalf("miss should be empty: %d %s", code, body)
	}
}
