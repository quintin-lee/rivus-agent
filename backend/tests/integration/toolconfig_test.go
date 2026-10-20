package integration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/runtime"
	"rivus-agent-backend/internal/tool"
	"rivus-agent-backend/tests/testutil"
)

// TestMCPPreApprovedToolRunsInline drives the runner directly: a pre-approved
// canned mcp__fake__echo call must execute inline — no approval requested —
// and the run must finish with both tool.completed and run.finished events.
func TestMCPPreApprovedToolRunsInline(t *testing.T) {
	h := testutil.NewToolConfigHarness(t)
	rawArgs := `{"msg":"hi"}`
	h.Model.EnqueueToolCall("tc1", "mcp__fake__echo", rawArgs)
	h.Model.EnqueueText("done")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ch, err := h.Runner.Run(ctx, runtime.RunRequest{
		RunID:         "mcp_pre1",
		Goal:          "use the echo tool",
		ApprovedTools: map[string]bool{"mcp__fake__echo:" + tool.ArgsHash(json.RawMessage(rawArgs)): true},
	})
	if err != nil {
		t.Fatal(err)
	}
	var sawToolCompleted, sawFinished bool
	for ev := range ch {
		switch ev.Type {
		case domain.EvtToolCompleted:
			if ev.ToolName != "mcp__fake__echo" {
				t.Fatalf("tool name = %q", ev.ToolName)
			}
			sawToolCompleted = true
		case domain.EvtRunFinished:
			sawFinished = true
		case domain.EvtApprovalRequested:
			t.Fatalf("pre-approved tool must NOT request approval")
		}
	}
	if !sawToolCompleted || !sawFinished {
		t.Fatalf("want tool.completed + run.finished, got completed=%v finished=%v", sawToolCompleted, sawFinished)
	}
}

// TestMCPToolApprovalRoundTrip mirrors approval_test.go's public-API path:
// a run whose scripted model calls the canned mcp__fake__echo WITHOUT
// pre-approval must park at waiting_approval, then approve → resume →
// succeeded, with the tool's tool.completed event in the run's stream.
func TestMCPToolApprovalRoundTrip(t *testing.T) {
	h := testutil.NewToolConfigHarness(t)
	rawArgs := `{"msg":"hi"}`
	// The public-API resume re-runs the model fresh (RunService.Resume →
	// rt.Execute → Runner.Run), so the fallback must re-issue the same tool
	// call on each segment and only finalize once a tool result is present
	// — mirroring approval_test.go's TestApprovalApproveResume.
	finalText := "输出风险列表：已确认（证据：mcp__fake__echo 结果），每条建议有文件或代码证据。"
	h.Model.SetFallback(func(_ int, input []*schema.Message) (*schema.Message, error) {
		for _, m := range input {
			if m.Role == schema.Tool {
				return &schema.Message{Role: schema.Assistant, Content: finalText}, nil
			}
		}
		return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
			ID: "tc1", Type: "function",
			Function: schema.FunctionCall{Name: "mcp__fake__echo", Arguments: rawArgs},
		}}}, nil
	})
	ses := h.CreateSession("s")
	id, code, _ := h.CreateRun(ses, fixturePayload(t, ses), "mcp-apr-1")
	if code != 201 {
		t.Fatalf("create run: %d", code)
	}
	a := waitApproval(t, h, id)
	if a.ToolName != "mcp__fake__echo" {
		t.Fatalf("approval tool = %q, want mcp__fake__echo", a.ToolName)
	}
	code, body := h.Do("POST", "/api/v1/runs/"+id+"/approvals/"+a.ID,
		map[string]any{"approve": true, "reason": "ok"}, nil)
	if code != 200 {
		t.Fatalf("decide: %d %s", code, body)
	}
	// The approval record must have flipped to approved (the decide path ran).
	recs, err := h.Approvals.ListByRun(context.Background(), id)
	if err != nil || len(recs) != 1 || recs[0].Status != domain.ApprovalApproved {
		t.Fatalf("want 1 approval record approved, got %+v err=%v", recs, err)
	}
	run := h.WaitTerminal(id, 30*time.Second)
	if run.Status != domain.RunSucceeded {
		t.Fatalf("want succeeded after approval, got %s (%s)", run.Status, run.ErrorSummary)
	}
	_, evBody := h.Do("GET", "/api/v1/runs/"+id+"/events", nil, nil)
	evs := string(evBody)
	if !strings.Contains(evs, "tool.completed") || !strings.Contains(evs, "mcp__fake__echo") {
		t.Fatalf("run event stream missing tool.completed/mcp__fake__echo: %s", evs)
	}
}

// TestInvokeSkillToolCompleted drives the runner directly: invoke_skill is
// a real skill-backed tool (read/never) — no approval — and must complete +
// finish the run. The SKILL.md body is not carried on the event channel, so
// the assertions are: tool.completed for invoke_skill, no approval event,
// run.finished.
func TestInvokeSkillToolCompleted(t *testing.T) {
	h := testutil.NewToolConfigHarness(t)
	h.Model.EnqueueToolCall("tc1", "invoke_skill", `{"name":"demo"}`)
	h.Model.EnqueueText("done")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ch, err := h.Runner.Run(ctx, runtime.RunRequest{
		RunID: "skill1",
		Goal:  "invoke the demo skill",
	})
	if err != nil {
		t.Fatal(err)
	}
	var sawToolCompleted, sawFinished, sawApproval bool
	for ev := range ch {
		switch ev.Type {
		case domain.EvtToolCompleted:
			if ev.ToolName == "invoke_skill" {
				sawToolCompleted = true
			}
		case domain.EvtRunFinished:
			sawFinished = true
		case domain.EvtApprovalRequested:
			sawApproval = true
		}
	}
	if sawApproval {
		t.Fatal("invoke_skill must NOT request approval")
	}
	if !sawToolCompleted {
		t.Fatal("want tool.completed for invoke_skill")
	}
	if !sawFinished {
		t.Fatal("want run.finished")
	}
}

// TestToolsSettingsRoundTrip proves the /settings/tools API surface: GET
// returns the provider's status shape and PUT reaches the provider with the
// recorded payload.
func TestToolsSettingsRoundTrip(t *testing.T) {
	h := testutil.NewToolConfigHarness(t)
	code, body := h.Do("GET", "/api/v1/settings/tools", nil, nil)
	if code != 200 {
		t.Fatalf("GET /settings/tools = %d: %s", code, body)
	}
	if !strings.Contains(string(body), `"skills"`) || !strings.Contains(string(body), `"mcp"`) {
		t.Fatalf("GET shape missing skills/mcp: %s", body)
	}
	if !strings.Contains(string(body), `"demo"`) {
		t.Fatalf("GET body missing demo skill: %s", body)
	}
	code, body = h.Do("PUT", "/api/v1/settings/tools", map[string]any{
		"skillsEnabled": true,
		"mcpEnabled":    true,
		"mcpServers":    []any{map[string]any{"name": "fs", "command": "npx", "args": []string{"-y"}}},
	}, nil)
	if code != 200 {
		t.Fatalf("PUT /settings/tools = %d: %s", code, body)
	}
	got := h.SavedToolConfigs()
	if len(got) != 1 {
		t.Fatalf("want exactly 1 recorded PUT, got %d: %+v", len(got), got)
	}
	if !got[0].SkillsEnabled || !got[0].MCPEnabled || len(got[0].MCPServers) != 1 {
		t.Fatalf("provider did not record the PUT payload: %+v", got)
	}
	if got[0].MCPServers[0].Name != "fs" || got[0].MCPServers[0].Command != "npx" || len(got[0].MCPServers[0].Args) != 1 {
		t.Fatalf("recorded mcpServer shape wrong: %+v", got[0].MCPServers[0])
	}
}
