package evals

import (
	"encoding/json"
	"strings"
	"testing"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/tool"
	"rivus-agent-backend/tests/testutil"
)

// 场景 35：GET /runs?q= 对 ≥3 字节（含中文）词条走 FTS5 短语路径，短 ASCII 词回退 LIKE——两路同一行集。
func TestScenarioRunSearchFTSCjk(t *testing.T) {
	Run(t, Scenario{
		Name: "run_search_fts_cjk", Category: "agent",
		Goal: "run search matches CJK phrases via the FTS index and short terms via LIKE",
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueText("好的，已收到。")
			return ""
		},
		Want: Expect{TerminalStatus: domain.RunSucceeded, MaxModelCalls: 1},
		PostCheck: func(t *testing.T, h *testutil.Harness, runID string, run domain.Run) {
			id, code, _ := h.CreateRun(run.SessionID, map[string]any{
				"session_id": run.SessionID,
				"goal":       "r25 全文检索 中文资料 xyz",
				"mode":       "react",
			}, "r25-fts-1")
			if code != 201 {
				t.Fatalf("create seed run: %d", code)
			}
			_ = id

			// 中文 ≥3 字节 → FTS 短语路径
			code, body := h.Do("GET", "/api/v1/runs?q=%E4%B8%AD%E6%96%87%E8%B5%84%E6%96%99", nil, nil)
			if code != 200 {
				t.Fatalf("cjk query: %d %s", code, body)
			}
			if !strings.Contains(string(body), "r25 全文检索") {
				t.Fatalf("cjk should hit seeded run: %s", body)
			}
			// FTS 只应命中种子 run，不命中本场景主 run
			var resp struct {
				Runs []struct {
					Goal string `json:"goal"`
				} `json:"runs"`
			}
			if err := json.Unmarshal(body, &resp); err != nil {
				t.Fatal(err)
			}
			if len(resp.Runs) != 1 {
				t.Fatalf("want exactly 1 hit, got %d: %v", len(resp.Runs), resp.Runs)
			}

			// 短 ASCII 词 → LIKE 兜底
			code, body = h.Do("GET", "/api/v1/runs?q=xyz", nil, nil)
			if code != 200 || !strings.Contains(string(body), "r25 全文检索") {
				t.Fatalf("short-term LIKE: %d %s", code, body)
			}

			// 错词不命中
			code, body = h.Do("GET", "/api/v1/runs?q=zzz-not-there", nil, nil)
			if code != 200 || strings.Contains(string(body), "r25 全文检索") {
				t.Fatalf("miss should be empty: %d %s", code, body)
			}
		},
	})
}

// 场景 36：PUT /settings/tools 校验 oauth 仅允许 http 型 MCP 服务器；stdio+oauth 拒绝。
func TestScenarioMCPOAuthStdioRejected(t *testing.T) {
	Run(t, Scenario{
		Name: "mcp_oauth_stdio_rejected", Category: "tool",
		Goal: "oauth config is rejected for stdio MCP servers",
		NewHarness: func(t testutil.TB, _ ...tool.Definition) *testutil.Harness {
			return testutil.NewToolConfigHarness(t)
		},
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueText("好的，已收到。")
			return ""
		},
		Want: Expect{TerminalStatus: domain.RunSucceeded, MaxModelCalls: 1},
		PostCheck: func(t *testing.T, h *testutil.Harness, runID string, run domain.Run) {
			payload := map[string]any{
				"skillsEnabled": true, "mcpEnabled": true,
				"mcpServers": []any{map[string]any{
					"name": "bad-stdio", "command": "echo",
					"oauth": map[string]any{
						"token_url": "http://127.0.0.1:9/token",
						"client_id": "rivus", "client_secret": "s3cret",
					},
				}},
			}
			code, body := h.Do("PUT", "/api/v1/settings/tools", payload, nil)
			if code != 400 {
				t.Fatalf("stdio+oauth should be 400, got %d: %s", code, body)
			}
			if !strings.Contains(string(body), "oauth is only supported for http servers") {
				t.Fatalf("unexpected error body: %s", body)
			}
			// 去掉 oauth 后同 payload 通过
			delete(payload["mcpServers"].([]any)[0].(map[string]any), "oauth")
			code, body = h.Do("PUT", "/api/v1/settings/tools", payload, nil)
			if code != 200 {
				t.Fatalf("stdio without oauth should pass: %d %s", code, body)
			}
		},
	})
}
