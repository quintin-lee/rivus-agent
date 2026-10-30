package evals

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/tests/testutil"
)

// 场景 37：GET /api/v1/sessions?q= 按标题大小写不敏感子串过滤（R18 语义的端到端评估）。
func TestScenarioSessionTitleSearch(t *testing.T) {
	Run(t, Scenario{
		Name: "session_title_search", Category: "tool",
		Goal: "session list filters by title substring via q",
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueText("好的，已收到。")
			return ""
		},
		Want: Expect{TerminalStatus: domain.RunSucceeded, MaxModelCalls: 1},
		PostCheck: func(t *testing.T, h *testutil.Harness, _ string, _ domain.Run) {
			h.CreateSession("r26-alpha-target")
			h.CreateSession("r26-beta-other")

			code, body := h.Do("GET", "/api/v1/sessions?q=alpha-target", nil, nil)
			if code != 200 {
				t.Fatalf("filter query: %d %s", code, body)
			}
			if !strings.Contains(string(body), "r26-alpha-target") {
				t.Fatalf("q=alpha-target should hit seeded session: %s", body)
			}
			if strings.Contains(string(body), "r26-beta-other") {
				t.Fatalf("q=alpha-target must not match the other session: %s", body)
			}

			code, body = h.Do("GET", "/api/v1/sessions", nil, nil)
			if code != 200 || !strings.Contains(string(body), "r26-beta-other") {
				t.Fatalf("no-q should list all: %d %s", code, body)
			}
		},
	})
}

// 场景 38：sessions 游标分页端到端——limit 截断 + next_cursor 翻页 + 无效游标 400（R22 语义）。
func TestScenarioCursorPagination(t *testing.T) {
	Run(t, Scenario{
		Name: "cursor_pagination", Category: "tool",
		Goal: "cursor pagination returns next_cursor, pages via cursor and rejects invalid cursors",
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueText("好的，已收到。")
			return ""
		},
		Want: Expect{TerminalStatus: domain.RunSucceeded, MaxModelCalls: 1},
		PostCheck: func(t *testing.T, h *testutil.Harness, _ string, _ domain.Run) {
			for i := 1; i <= 3; i++ {
				h.CreateSession(fmt.Sprintf("r26-page-%d", i))
				time.Sleep(2 * time.Millisecond) // ms 精度：确保 updated_at 可分页
			}

			code, body := h.Do("GET", "/api/v1/sessions?limit=2", nil, nil)
			if code != 200 {
				t.Fatalf("page1: %d %s", code, body)
			}
			var page struct {
				Sessions []struct {
					Title string `json:"title"`
				} `json:"sessions"`
				NextCursor string `json:"next_cursor"`
			}
			if err := json.Unmarshal(body, &page); err != nil {
				t.Fatal(err)
			}
			if len(page.Sessions) != 2 {
				t.Fatalf("limit=2 want 2 rows, got %d", len(page.Sessions))
			}
			if page.NextCursor == "" {
				t.Fatalf("full page must carry next_cursor: %s", body)
			}
			if page.Sessions[0].Title != "r26-page-3" {
				t.Fatalf("newest first expected r26-page-3, got %q", page.Sessions[0].Title)
			}

			code, body = h.Do("GET", "/api/v1/sessions?limit=2&cursor="+url.QueryEscape(page.NextCursor), nil, nil)
			if code != 200 {
				t.Fatalf("page2: %d %s", code, body)
			}
			if !strings.Contains(string(body), "r26-page-1") || strings.Contains(string(body), "r26-page-3") {
				t.Fatalf("page2 should contain only the remaining session: %s", body)
			}

			code, _ = h.Do("GET", "/api/v1/sessions?limit=2&cursor=%21%21%21bad", nil, nil)
			if code != 400 {
				t.Fatalf("invalid cursor want 400, got %d", code)
			}
		},
	})
}
