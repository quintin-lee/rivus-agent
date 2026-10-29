package evals

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/tests/testutil"
)

// 场景 33：GET /sessions?offset= 分页——排除最新、越界空列表、无 offset 全量。
func TestScenarioSessionPagination(t *testing.T) {
	Run(t, Scenario{
		Name: "session_pagination", Category: "agent",
		Goal: "session listing must honor offset pagination with newest-first order",
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueText("好的，已收到。")
			return ""
		},
		Want: Expect{TerminalStatus: domain.RunSucceeded, MaxModelCalls: 1},
		PostCheck: func(t *testing.T, h *testutil.Harness, runID string, run domain.Run) {
			for i := 1; i <= 3; i++ {
				h.CreateSession("r21-page-" + string(rune('0'+i)))
				time.Sleep(2 * time.Millisecond) // ms 精度：避免 updated_at 同值打乱顺序
			}
			code, body := h.Do("GET", "/api/v1/sessions", nil, nil)
			if code != 200 {
				t.Fatalf("list: %d %s", code, body)
			}
			code, body = h.Do("GET", "/api/v1/sessions?offset=1", nil, nil)
			if code != 200 {
				t.Fatalf("offset=1: %d %s", code, body)
			}
			var resp1 struct {
				Sessions []struct {
					Title string `json:"title"`
				} `json:"sessions"`
			}
			if err := json.Unmarshal(body, &resp1); err != nil {
				t.Fatal(err)
			}
			// offset=1 跳过最新一条：不应包含最后创建的 r21-page-3
			for _, s := range resp1.Sessions {
				if s.Title == "r21-page-3" {
					t.Fatalf("offset=1 should skip newest, got %v", resp1.Sessions)
				}
			}
			code, body = h.Do("GET", "/api/v1/sessions?offset=99", nil, nil)
			if code != 200 || !strings.Contains(string(body), `"sessions":[]`) {
				t.Fatalf("offset out of range: %d %s", code, body)
			}
		},
	})
}

// 场景 34：GET /runs?q= 按 goal 大小写不敏感过滤；省略 q 返回全部。
func TestScenarioRunGoalSearch(t *testing.T) {
	Run(t, Scenario{
		Name: "run_goal_search", Category: "agent",
		Goal: "run listing must filter by goal substring via q param",
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueText("好的，已收到。")
			return ""
		},
		Want: Expect{TerminalStatus: domain.RunSucceeded, MaxModelCalls: 1},
		PostCheck: func(t *testing.T, h *testutil.Harness, runID string, run domain.Run) {
			ses := run.SessionID
			if _, code, _ := h.CreateRun(ses, map[string]any{
				"session_id": ses, "goal": "r21-marker-alpha 整理", "mode": "react",
			}, "r21-search-a"); code != 201 {
				t.Fatalf("create run a: %d", code)
			}
			if _, code, _ := h.CreateRun(ses, map[string]any{
				"session_id": ses, "goal": "r21-marker-beta 归档", "mode": "react",
			}, "r21-search-b"); code != 201 {
				t.Fatalf("create run b: %d", code)
			}

			code, body := h.Do("GET", "/api/v1/runs?q=marker-alpha", nil, nil)
			if code != 200 {
				t.Fatalf("q filter: %d %s", code, body)
			}
			if !strings.Contains(string(body), "r21-marker-alpha") || strings.Contains(string(body), "r21-marker-beta") {
				t.Fatalf("filter failed: %s", body)
			}
			code, body = h.Do("GET", "/api/v1/runs", nil, nil)
			if code != 200 || !strings.Contains(string(body), "r21-marker-beta") {
				t.Fatalf("no-q should return all: %d %s", code, body)
			}
		},
	})
}
