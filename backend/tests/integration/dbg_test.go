package integration

import (
	"context"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"rivus-agent-backend/tests/testutil"
)

func TestDebugApprove(t *testing.T) {
	h := testutil.New(t, publishToolDef())
	finalText := "输出风险列表：已发布（证据：publish_draft 结果），每条建议有文件或代码证据。"
	h.Model.EnqueueFunc(func(_ int, input []*schema.Message) (*schema.Message, error) {
		for _, m := range input {
			t.Logf("model input role=%s content=%.60q toolcalls=%d", m.Role, m.Content, len(m.ToolCalls))
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
	id, _, _ := h.CreateRun(ses, map[string]any{"goal": "publish", "success_criteria": []string{"输出风险列表"}}, "dbg-1")
	run := h.WaitTerminal(id, 30*time.Second)
	t.Logf("after first: status=%s calls=%d", run.Status, h.Model.Calls())
	recs, _ := h.Approvals.ListByRun(context.Background(), id)
	for _, r := range recs {
		t.Logf("approval id=%s tool=%s callid=%s hash=%s status=%s", r.ID, r.ToolName, r.ToolCallID, r.ArgsHash, r.Status)
	}
	code, body := h.Do("POST", "/api/v1/runs/"+id+"/approvals/"+recs[0].ID, map[string]any{"approve": true}, nil)
	t.Logf("decide: %d %s", code, body)
	run2 := h.WaitTerminal(id, 30*time.Second)
	t.Logf("after resume: status=%s err=%s result=%.200q calls=%d", run2.Status, run2.ErrorSummary, run2.ResultJSON, h.Model.Calls())
	evs, _ := h.Events.ListAfter(context.Background(), id, 0, 100)
	for _, e := range evs {
		t.Logf("event %d %s %.120q", e.Seq, e.Type, e.PayloadJSON)
	}
}
