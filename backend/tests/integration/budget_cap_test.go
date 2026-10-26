package integration

import (
	"context"
	"testing"
	"time"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/tests/testutil"
)

func budgetPayload(t *testing.T, ses string, overrides map[string]any) map[string]any {
	t.Helper()
	payload := fixturePayload(t, ses)
	b, ok := payload["budget"].(map[string]any)
	if !ok {
		t.Fatalf("fixture payload budget missing or wrong type: %#v", payload["budget"])
	}
	for k, v := range overrides {
		b[k] = v
	}
	return payload
}

func eventTypes(t *testing.T, h *testutil.Harness, runID string) []string {
	t.Helper()
	evs, err := h.Events.ListAfter(context.Background(), runID, 0, 500)
	if err != nil {
		t.Fatalf("ListAfter: %v", err)
	}
	out := make([]string, 0, len(evs))
	for _, ev := range evs {
		out = append(out, string(ev.Type))
	}
	return out
}

func containsType(types []string, want string) bool {
	for _, s := range types {
		if s == want {
			return true
		}
	}
	return false
}

// 工具预算触顶：run 必须终态 RunFailed + error_code=tool_calls_budget_exceeded + budget.exceeded 事件。
func TestBudgetCapToolCalls(t *testing.T) {
	h := testutil.New(t)
	h.Model.EnqueueToolCall("tc1", "knowledge_search", `{"query":"q1","top_k":3}`)
	h.Model.EnqueueText("最终答案")
	ses := h.CreateSession("s")

	payload := budgetPayload(t, ses, map[string]any{"max_tool_calls": 1})
	id, code, _ := h.CreateRun(ses, payload, "bc-tool-1")
	if code != 201 {
		t.Fatalf("CreateRun code = %d, want 201", code)
	}
	run := h.WaitTerminal(id, 30*time.Second)
	if run.Status != domain.RunFailed {
		t.Fatalf("status = %s, want failed (events=%v)", run.Status, eventTypes(t, h, id))
	}
	if run.ErrorCode != "tool_calls_budget_exceeded" {
		t.Fatalf("error_code = %q, want tool_calls_budget_exceeded (events=%v)",
			run.ErrorCode, eventTypes(t, h, id))
	}
	if !containsType(eventTypes(t, h, id), string(domain.EvtBudgetExceeded)) {
		t.Fatalf("missing budget.exceeded event; events = %v", eventTypes(t, h, id))
	}
}

// 模型预算触顶：第一次 model.completed 即触限。
func TestBudgetCapModelCalls(t *testing.T) {
	h := testutil.New(t)
	h.Model.EnqueueText("第一轮回答")
	ses := h.CreateSession("s")

	payload := budgetPayload(t, ses, map[string]any{"max_model_calls": 1})
	id, code, _ := h.CreateRun(ses, payload, "bc-model-1")
	if code != 201 {
		t.Fatalf("CreateRun code = %d, want 201", code)
	}
	run := h.WaitTerminal(id, 30*time.Second)
	if run.Status != domain.RunFailed || run.ErrorCode != "model_calls_budget_exceeded" {
		t.Fatalf("status=%s error_code=%q; want failed/model_calls_budget_exceeded (events=%v)",
			run.Status, run.ErrorCode, eventTypes(t, h, id))
	}
	if !containsType(eventTypes(t, h, id), string(domain.EvtBudgetExceeded)) {
		t.Fatalf("missing budget.exceeded event; events = %v", eventTypes(t, h, id))
	}
}

// 对照组：宽松预算下不得出现 budget.exceeded（防误杀）。
func TestBudgetNoFalsePositive(t *testing.T) {
	h := testutil.New(t)
	h.Model.EnqueueToolCall("tc1", "knowledge_search", `{"query":"q1","top_k":3}`)
	h.Model.EnqueueText("最终答案")
	ses := h.CreateSession("s")

	payload := budgetPayload(t, ses, map[string]any{"max_tool_calls": 10, "max_model_calls": 10})
	id, code, _ := h.CreateRun(ses, payload, "bc-ctl-1")
	if code != 201 {
		t.Fatalf("CreateRun code = %d, want 201", code)
	}
	_ = h.WaitTerminal(id, 30*time.Second)
	if containsType(eventTypes(t, h, id), string(domain.EvtBudgetExceeded)) {
		t.Fatalf("budget.exceeded fired under loose budget; events = %v", eventTypes(t, h, id))
	}
}
