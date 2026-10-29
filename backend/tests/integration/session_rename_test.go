package integration

import (
	"net/http"
	"strings"
	"testing"

	"rivus-agent-backend/tests/testutil"
)

func TestSessionRename(t *testing.T) {
	h := testutil.New(t)
	ses := h.CreateSession("old-name")

	// 改名 → 200，回读为 200 {session_id,title,...}
	code, body := h.Do(http.MethodPatch, "/api/v1/sessions/"+ses,
		map[string]string{"title": "renamed"}, nil)
	if code != 200 {
		t.Fatalf("patch: %d %s", code, body)
	}
	_, getBody := h.Do(http.MethodGet, "/api/v1/sessions/"+ses, nil, nil)
	if !strings.Contains(string(getBody), "renamed") {
		t.Fatalf("get after rename body = %s", getBody)
	}

	// 空 title → 400 bad_request
	code2, _ := h.Do(http.MethodPatch, "/api/v1/sessions/"+ses,
		map[string]string{"title": "   "}, nil)
	if code2 != 400 {
		t.Fatalf("empty title: %d", code2)
	}

	// 不存在 → 404
	code3, _ := h.Do(http.MethodPatch, "/api/v1/sessions/ses_missing",
		map[string]string{"title": "x"}, nil)
	if code3 != 404 {
		t.Fatalf("missing: %d", code3)
	}
}
