package evals

import (
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/tests/testutil"
)

func finalWithEvidence() *schema.Message {
	return &schema.Message{Role: schema.Assistant,
		Content: "输出风险列表：N+1 查询（证据：knowledge_search 结果），每条建议有文件或代码证据。"}
}

func TestEvalEvidenceSuccess(t *testing.T) {
	h := testutil.New(t)
	h.Model.EnqueueToolCall("tc1", "knowledge_search", `{"query":"重构"}`)
	h.Model.EnqueueText(finalWithEvidence().Content)
	ses := h.CreateSession("eval")
	id, _, _ := h.CreateRun(ses, map[string]any{
		"goal":             "分析并输出风险列表",
		"success_criteria": []string{"输出风险列表", "每条建议有文件或代码证据"},
	}, "eval-ok")
	run := h.WaitTerminal(id, 30*time.Second)
	if run.Status != domain.RunSucceeded || !strings.Contains(run.ResultJSON, `"passed":true`) {
		t.Fatalf("got %s %s", run.Status, run.ResultJSON)
	}
}

func TestEvalNoEvidenceFailure(t *testing.T) {
	h := testutil.New(t)
	h.Model.EnqueueText("一切正常，没有问题。")
	ses := h.CreateSession("eval")
	id, _, _ := h.CreateRun(ses, map[string]any{
		"goal":             "分析并输出风险列表",
		"success_criteria": []string{"输出风险列表"},
	}, "eval-fail")
	run := h.WaitTerminal(id, 30*time.Second)
	if run.Status != domain.RunFailed || !strings.Contains(run.ResultJSON, "输出风险列表") {
		t.Fatalf("got %s %s", run.Status, run.ResultJSON)
	}
}

func TestEvalPlanExecuteSteps(t *testing.T) {
	h := testutil.New(t)
	h.Model.EnqueueToolCall("tc1", "knowledge_search", `{"query":"x"}`)
	h.Model.EnqueueText(finalWithEvidence().Content)
	ses := h.CreateSession("eval")
	id, _, _ := h.CreateRun(ses, map[string]any{
		"goal": "分阶段完成多步骤计划任务",
		"mode": "plan_execute",
	}, "eval-plan")
	run := h.WaitTerminal(id, 30*time.Second)
	_, steps := h.GetRun(id)
	if len(steps) == 0 {
		t.Fatalf("plan_execute must create steps, run=%s", run.Status)
	}
}

func TestEvalBudgetTerminates(t *testing.T) {
	h := testutil.New(t)
	h.Model.SetFallback(func(int, []*schema.Message) (*schema.Message, error) {
		return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
			ID: "tc", Type: "function",
			Function: schema.FunctionCall{Name: "knowledge_search", Arguments: `{"query":"loop"}`},
		}}}, nil
	})
	ses := h.CreateSession("eval")
	id, _, _ := h.CreateRun(ses, map[string]any{
		"goal":   "loop forever",
		"budget": map[string]any{"max_iterations": 1, "max_model_calls": 3, "max_tool_calls": 10, "max_duration_seconds": 60},
	}, "eval-budget")
	run := h.WaitTerminal(id, 60*time.Second)
	if !run.Status.Terminal() {
		t.Fatalf("budget must terminate run, got %s", run.Status)
	}
	if calls := h.Model.Calls(); calls > 6 {
		t.Fatalf("model calls unbounded: %d", calls)
	}
}
