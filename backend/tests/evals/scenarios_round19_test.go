package evals

import (
	"strings"
	"testing"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/tests/testutil"
)

// 场景 31：PATCH /sessions/{id} 重命名持久化；GET 列表可读回；空标题 400。
func TestScenarioSessionRenamePersists(t *testing.T) {
	Run(t, Scenario{
		Name: "session_rename_persists", Category: "tool",
		Goal: "renaming a session via PATCH must persist and reject empty titles",
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueText("好的，已收到。")
			return ""
		},
		Want: Expect{TerminalStatus: domain.RunSucceeded, MaxModelCalls: 1},
		PostCheck: func(t *testing.T, h *testutil.Harness, runID string, run domain.Run) {
			sid := run.SessionID
			code, body := h.Do("PATCH", "/api/v1/sessions/"+sid, map[string]any{"title": "renamed-eval"}, nil)
			if code != 200 || !strings.Contains(string(body), "renamed-eval") {
				t.Fatalf("rename: %d %s", code, body)
			}
			code, body = h.Do("GET", "/api/v1/sessions", nil, nil)
			if code != 200 || !strings.Contains(string(body), "renamed-eval") {
				t.Fatalf("get after rename: %d %s", code, body)
			}
			code, _ = h.Do("PATCH", "/api/v1/sessions/"+sid, map[string]any{"title": "   "}, nil)
			if code != 400 {
				t.Fatalf("empty title: want 400, got %d", code)
			}
		},
	})
}

// 场景 32：GET /sessions?q= 按标题大小写不敏感过滤；省略 q 返回全部。
func TestScenarioSessionSearchFilters(t *testing.T) {
	Run(t, Scenario{
		Name: "session_search_filters", Category: "tool",
		Goal: "searching sessions via the q param must filter by title substring",
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueText("好的。")
			return ""
		},
		Want: Expect{TerminalStatus: domain.RunSucceeded, MaxModelCalls: 1},
		PostCheck: func(t *testing.T, h *testutil.Harness, runID string, run domain.Run) {
			h.CreateSession("r19-search-target-xyz")
			h.CreateSession("r19-search-other-abc")
			code, body := h.Do("GET", "/api/v1/sessions?q=target-xyz", nil, nil)
			if code != 200 {
				t.Fatalf("search: %d", code)
			}
			if !strings.Contains(string(body), "r19-search-target-xyz") {
				t.Fatalf("expected hit: %s", body)
			}
			if strings.Contains(string(body), "r19-search-other-abc") {
				t.Fatalf("unexpected hit: %s", body)
			}
			code, body = h.Do("GET", "/api/v1/sessions", nil, nil)
			if code != 200 || !strings.Contains(string(body), "r19-search-other-abc") {
				t.Fatalf("no-q must list all: %d %s", code, body)
			}
		},
	})
}
