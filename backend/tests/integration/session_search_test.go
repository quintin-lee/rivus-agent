package integration

import (
	"strings"
	"testing"

	"rivus-agent-backend/tests/testutil"
)

func TestSessionSearch(t *testing.T) {
	h := testutil.New(t)
	h.CreateSession("Alpha 项目")
	h.CreateSession("beta notes")

	code, body := h.Do("GET", "/api/v1/sessions?q=alpha", nil, nil)
	if code != 200 {
		t.Fatalf("code=%d body=%s", code, body)
	}
	if !strings.Contains(string(body), "Alpha 项目") || strings.Contains(string(body), "beta notes") {
		t.Fatalf("filter failed: %s", body)
	}

	code, body = h.Do("GET", "/api/v1/sessions", nil, nil)
	if code != 200 || !strings.Contains(string(body), "beta notes") {
		t.Fatalf("no-q should return all: %d %s", code, body)
	}

	code, body = h.Do("GET", "/api/v1/sessions?q=zzz-not-there", nil, nil)
	if code != 200 || strings.Contains(string(body), "Alpha 项目") {
		t.Fatalf("miss should return empty list: %d %s", code, body)
	}
}
