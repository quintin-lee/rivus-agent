package evals

import (
	"context"
	"strings"
	"testing"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/tool"
	"rivus-agent-backend/tests/testutil"
)

// 场景 43：MCP OAuth 授权码校验——authorize_url 非空但缺 redirect_port → 400；补齐 → 200（R28 ValidateSpecs）。
func TestScenarioMCPOAuthAuthcodeValidation(t *testing.T) {
	Run(t, Scenario{
		Name: "mcp_oauth_authcode_validation", Category: "tool",
		Goal: "oauth authorize_url requires redirect_port",
		NewHarness: func(t testutil.TB, _ ...tool.Definition) *testutil.Harness {
			return testutil.NewToolConfigHarness(t)
		},
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueText("好的。")
			return ""
		},
		Want: Expect{TerminalStatus: domain.RunSucceeded, MaxModelCalls: 1},
		PostCheck: func(t *testing.T, h *testutil.Harness, _ string, _ domain.Run) {
			srv := func(oauth map[string]any) []map[string]any {
				return []map[string]any{{
					"name": "r31-authcode", "type": "http", "url": "https://mcp.example.com/rpc",
					"oauth": oauth,
				}}
			}
			put := func(oauth map[string]any) int {
				code, body := h.Do("PUT", "/api/v1/settings/tools", map[string]any{
					"skillsEnabled": false, "mcpEnabled": true,
					"mcpServers": srv(oauth),
				}, nil)
				if code == 500 {
					t.Fatalf("PUT returned 500 (should be 4xx on invalid config): %s", body)
				}
				return code
			}

			// 授权码缺 redirect_port → 400
			if code := put(map[string]any{
				"token_url": "https://auth.example.com/token",
				"client_id": "r31", "client_secret": "r31-secret",
				"authorize_url": "https://auth.example.com/authorize",
			}); code != 400 {
				t.Fatalf("authorize_url without redirect_port: code=%d want 400", code)
			}

			// 补齐 redirect_port → 200
			if code := put(map[string]any{
				"token_url": "https://auth.example.com/token",
				"client_id": "r31", "client_secret": "r31-secret",
				"authorize_url": "https://auth.example.com/authorize",
				"redirect_port": 8123,
			}); code != 200 {
				t.Fatalf("full auth-code oauth: code=%d want 200", code)
			}
		},
	})
}

// 场景 44：默认（无 S3 endpoint）下 s3_* 工具未注册——模型调用 s3_read 得到
// tool_not_found（若被误注册则会返回 "s3 client not initialized"），事件流中
// 无 s3_read 的 tool.completed（防 R30 条件注册回归）。
func TestScenarioS3ToolsAbsentByDefault(t *testing.T) {
	Run(t, Scenario{
		Name: "s3_tools_absent_by_default", Category: "agent",
		Goal: "read s3 object (default config, no s3 endpoint)",
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueToolCall("tc-s3", "s3_read", `{"key":"a.txt"}`)
			return ""
		},
		Want: Expect{TerminalStatus: domain.RunFailed},
		PostCheck: func(t *testing.T, h *testutil.Harness, runID string, _ domain.Run) {
			evs, err := h.Events.ListAfter(context.Background(), runID, 0, 500)
			if err != nil {
				t.Fatalf("list events: %v", err)
			}
			failed := false
			for _, ev := range evs {
				switch ev.Type {
				case domain.EvtModelFailed:
					// 未知工具错误以 model.failed 形式进入事件流（adk toolsNode 缺工具 → 节点错误）。
					if !strings.Contains(ev.PayloadJSON, "s3_read") {
						t.Fatalf("model.failed payload lacks s3_read: %s", ev.PayloadJSON)
					}
					failed = true
				case domain.EvtToolCompleted:
					if strings.Contains(ev.PayloadJSON, "s3_read") {
						t.Fatalf("s3_read tool.completed present although S3 not configured: %s", ev.PayloadJSON)
					}
				}
			}
			if !failed {
				t.Fatal("no s3_read failure event in stream")
			}
		},
	})
}

// 场景 45：file_edit 审批决策流——批准 → 终态 succeeded 且事件含 approval.decided。
func TestScenarioApprovalDecidedFlow(t *testing.T) {
	ws := withTempWorkspace(t)
	if err := writeFileRel(ws, "svc/b.go", "old\n"); err != nil {
		t.Fatal(err)
	}
	Run(t, Scenario{
		Name: "approval_decided_flow", Category: "agent",
		Goal: "edit svc/b.go with approval",
		Script: func(h *testutil.Harness) string {
			h.Model.SetFallback(reissueToolThenText("tc-b", "file_edit",
				`{"path":"svc/b.go","old_text":"old","new_text":"new"}`,
				"edit done"))
			return ""
		},
		Want: Expect{
			TerminalStatus: domain.RunSucceeded,
			RequiredEvents: []domain.EventType{
				domain.EvtApprovalRequested, domain.EvtApprovalDecided,
			},
		},
		MidRun: func(t *testing.T, h *testutil.Harness, runID string) {
			aID := firstApprovalID(t, h, runID)
			code, body := h.Do("POST", "/api/v1/runs/"+runID+"/approvals/"+aID,
				map[string]any{"approve": true, "reason": "r31"}, nil)
			if code != 200 {
				t.Fatalf("approve: %d %s", code, body)
			}
		},
	})
}
