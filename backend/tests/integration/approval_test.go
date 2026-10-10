package integration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/tool"
	"rivus-agent-backend/tests/testutil"
)

func publishToolDef() tool.Definition {
	return tool.Definition{
		Name: "publish_draft", Description: "publishes a draft (test only, always needs approval)", Version: "v1",
		InputSchema:    `{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}`,
		Risk:           tool.RiskExternal,
		Approval:       tool.ApprovalAlways,
		TimeoutMs:      10000,
		MaxResultBytes: 1024,
		Handler: func(context.Context, json.RawMessage) (string, error) {
			return `{"published":true}`, nil
		},
	}
}

func waitApproval(t *testing.T, h *testutil.Harness, id string) domain.Approval {
	t.Helper()
	run := h.WaitTerminal(id, 30*time.Second)
	if run.Status != domain.RunWaitingApproval {
		t.Fatalf("want waiting_approval, got %s (%s)", run.Status, run.ErrorSummary)
	}
	records, err := h.Approvals.ListByRun(context.Background(), id)
	if err != nil || len(records) != 1 {
		t.Fatalf("want 1 approval record, got %v err=%v", len(records), err)
	}
	if records[0].Status != domain.ApprovalPending {
		t.Fatalf("want pending, got %s", records[0].Status)
	}
	return records[0]
}

func TestApprovalApproveResume(t *testing.T) {
	h := testutil.New(t, publishToolDef())
	finalText := "输出风险列表：已发布（证据：publish_draft 结果），每条建议有文件或代码证据。"
	h.Model.SetFallback(func(_ int, input []*schema.Message) (*schema.Message, error) {
		for _, m := range input {
			if m.Role == schema.Tool {
				return &schema.Message{Role: schema.Assistant, Content: finalText}, nil
			}
		}
		return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
			ID: "tc1", Type: "function",
			Function: schema.FunctionCall{Name: "publish_draft", Arguments: `{"title":"hello"}`},
		}}}, nil
	})
	ses := h.CreateSession("s")
	payload := fixturePayload(t, ses)
	id, code, _ := h.CreateRun(ses, payload, "apr-ok-1")
	if code != 201 {
		t.Fatalf("create run: %d", code)
	}
	a := waitApproval(t, h, id)
	code, body := h.Do("POST", "/api/v1/runs/"+id+"/approvals/"+a.ID,
		map[string]any{"approve": true, "reason": "looks good"}, nil)
	if code != 200 {
		t.Fatalf("decide: %d %s", code, body)
	}
	run := h.WaitTerminal(id, 30*time.Second)
	if run.Status != domain.RunSucceeded {
		t.Fatalf("want succeeded after approval, got %s (%s)", run.Status, run.ErrorSummary)
	}
	_, evBody := h.Do("GET", "/api/v1/runs/"+id+"/events", nil, nil)
	if !contains(string(evBody), "approval.decided") {
		t.Fatalf("missing approval.decided event")
	}
}

func TestApprovalReject(t *testing.T) {
	h := testutil.New(t, publishToolDef())
	h.Model.EnqueueToolCall("tc1", "publish_draft", `{"title":"hello"}`)
	h.Model.EnqueueText("草稿待发布，审批中。")
	ses := h.CreateSession("s")
	id, _, _ := h.CreateRun(ses, map[string]any{"goal": "publish it"}, "apr-no-1")
	a := waitApproval(t, h, id)
	code, body := h.Do("POST", "/api/v1/runs/"+id+"/approvals/"+a.ID,
		map[string]any{"approve": false, "reason": "nope"}, nil)
	if code != 200 {
		t.Fatalf("decide: %d %s", code, body)
	}
	run := h.WaitTerminal(id, 30*time.Second)
	if run.Status != domain.RunFailed || run.ErrorCode != "approval_rejected" {
		t.Fatalf("want failed/approval_rejected, got %s/%s", run.Status, run.ErrorCode)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
