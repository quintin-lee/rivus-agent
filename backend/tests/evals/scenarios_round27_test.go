package evals

import (
	"strconv"
	"strings"
	"testing"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/tool"
	"rivus-agent-backend/tests/testutil"
)

// 场景 39：MCP OAuth 配置端到端——PUT 接受 http+oauth，GET 回显 oauth 且永不回显 client_secret（R24 语义）。
func TestScenarioMCPOAuthConfigRoundtrip(t *testing.T) {
	Run(t, Scenario{
		Name: "mcp_oauth_config_roundtrip", Category: "tool",
		Goal: "mcp server oauth config persists via PUT and echoes without client_secret",
		NewHarness: func(t testutil.TB, _ ...tool.Definition) *testutil.Harness {
			return testutil.NewToolConfigHarness(t)
		},
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueText("好的，已收到。")
			return ""
		},
		Want: Expect{TerminalStatus: domain.RunSucceeded, MaxModelCalls: 1},
		PostCheck: func(t *testing.T, h *testutil.Harness, _ string, _ domain.Run) {
			cfg := map[string]any{
				"skillsEnabled": true,
				"mcpEnabled":    true,
				"mcpServers": []map[string]any{{
					"name": "r27-oauth", "type": "http", "url": "https://mcp.example.com/rpc",
					"oauth": map[string]any{
						"token_url": "https://auth.example.com/token",
						"client_id": "r27-client", "client_secret": "r27-secret",
					},
				}},
			}
			code, body := h.Do("PUT", "/api/v1/settings/tools", cfg, nil)
			if code != 200 {
				t.Fatalf("PUT with oauth: %d %s", code, body)
			}

			// PUT 记录校验（GET 回显的是 manager 实时状态，PUT 载荷经 SavedToolConfigs 断言）
			saved := h.SavedToolConfigs()
			if len(saved) == 0 || len(saved[len(saved)-1].MCPServers) == 0 {
				t.Fatalf("PUT payload not recorded: %+v", saved)
			}
			oauthIn := saved[len(saved)-1].MCPServers[0].OAuth
			if oauthIn == nil || oauthIn.TokenURL != "https://auth.example.com/token" ||
				oauthIn.ClientID != "r27-client" || oauthIn.ClientSecret != "r27-secret" {
				t.Fatalf("recorded oauth mismatch: %+v", oauthIn)
			}

			// GET 不回显 client_secret
			code, body = h.Do("GET", "/api/v1/settings/tools", nil, nil)
			if code != 200 {
				t.Fatalf("GET tools: %d %s", code, body)
			}
			if strings.Contains(string(body), "r27-secret") {
				t.Fatalf("client_secret must never be echoed: %s", body)
			}
		},
	})
}

// 场景 40：事件 after 游标——?after=<seq> 仅返回更晚事件（断连重放语义）。
func TestScenarioEventsAfterCursor(t *testing.T) {
	Run(t, Scenario{
		Name: "events_after_cursor", Category: "tool",
		Goal: "events endpoint honors after cursor to replay only newer events",
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueText("好的，已完成。")
			return ""
		},
		Want: Expect{TerminalStatus: domain.RunSucceeded, MaxModelCalls: 1},
		PostCheck: func(t *testing.T, h *testutil.Harness, _ string, run domain.Run) {
			code, body := h.Do("GET", "/api/v1/runs/"+run.ID+"/events", nil, nil)
			if code != 200 {
				t.Fatalf("list events: %d %s", code, body)
			}
			ids := sseIDs(string(body))
			if len(ids) == 0 {
				t.Fatalf("no SSE events: %s", body)
			}
			last := ids[len(ids)-1]

			code, body = h.Do("GET", "/api/v1/runs/"+run.ID+"/events?after="+strconv.FormatInt(last, 10), nil, nil)
			if code != 200 {
				t.Fatalf("after query: %d %s", code, body)
			}
			for _, id := range sseIDs(string(body)) {
				if id <= last {
					t.Fatalf("after=%d returned older event id=%d", last, id)
				}
			}
		},
	})
}

// sseIDs 提取 SSE 文本中全部 id: 行的序号（事件端点返回 text/event-stream）。
func sseIDs(sse string) []int64 {
	var out []int64
	for _, line := range strings.Split(sse, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "id:") {
			continue
		}
		if n, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "id:")), 10, 64); err == nil {
			out = append(out, n)
		}
	}
	return out
}
