package integration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/tests/testutil"
)

// 单 attempt 夹具:knowledge_search(内置只读 ApprovalNever,无审批,run 在一个
// run() 调用内完成 → 计数单调)。断言基于 DB(非 SSE 时序),故不用 openStream/waitLine。
// 不要改用 park/approve(publishToolDef)夹具:approve 后的 Resume 是第二个 run()
// 调用,计数器从 0 重起,会破坏下面的"单调 + 终值==DB总计数"不变量。
func TestBudgetConsumedEmit(t *testing.T) {
	h := testutil.New(t)
	h.Model.EnqueueToolCall("tc1", "knowledge_search", `{"query":"重构","top_k":3}`)
	h.Model.EnqueueText("输出风险列表:N+1 查询(证据:knowledge_search 结果),每条建议有证据。")
	ses := h.CreateSession("s")
	id, code, _ := h.CreateRun(ses, fixturePayload(t, ses), "bc-1")
	if code != 201 {
		t.Fatalf("create run: %d", code)
	}
	run := h.WaitTerminal(id, 30*time.Second)
	if run.Status != domain.RunSucceeded {
		t.Fatalf("want succeeded, got %s (%s)", run.Status, run.ErrorSummary)
	}

	all, err := h.Events.ListAfter(context.Background(), id, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	var modelCompleted, toolCompleted, consumed []domain.AgentEvent
	for _, ev := range all {
		switch ev.Type {
		case domain.EvtModelCompleted:
			modelCompleted = append(modelCompleted, ev)
		case domain.EvtToolCompleted:
			toolCompleted = append(toolCompleted, ev)
		case domain.EvtBudgetConsumed:
			consumed = append(consumed, ev)
		}
	}

	// 非空真:确有 tool + model 边界
	// (实测:模型每轮各发 1 条 model.completed → 工具轮 + 文本轮共 2 条;
	//  文本轮后循环结束,无兜底 "done" 调用)
	if len(modelCompleted) != 2 {
		t.Fatalf("model.completed count = %d, want 2", len(modelCompleted))
	}
	if len(toolCompleted) != 1 {
		t.Fatalf("tool.completed count = %d, want 1", len(toolCompleted))
	}

	// budget.consumed 行数 == 边界总数(每条 completed 恰发 1 行;本夹具为 3)
	if len(consumed) != len(modelCompleted)+len(toolCompleted) {
		t.Fatalf("budget.consumed count = %d, want %d", len(consumed), len(modelCompleted)+len(toolCompleted))
	}

	// 单调不减 + 终值 == DB 总计数 + payload 三键齐全
	var lastMC, lastTC int
	for i, ev := range consumed {
		var p struct {
			ModelCalls      int `json:"model_calls"`
			ToolCalls       int `json:"tool_calls"`
			DurationSeconds int `json:"duration_seconds"`
		}
		if err := json.Unmarshal([]byte(ev.PayloadJSON), &p); err != nil {
			t.Fatalf("row %d: unmarshal: %v", i, err)
		}
		if p.DurationSeconds < 0 {
			t.Fatalf("row %d: duration_seconds negative: %d", i, p.DurationSeconds)
		}
		if p.ModelCalls < lastMC || p.ToolCalls < lastTC {
			t.Fatalf("row %d: non-monotone (%d,%d) < prev (%d,%d)", i, p.ModelCalls, p.ToolCalls, lastMC, lastTC)
		}
		lastMC, lastTC = p.ModelCalls, p.ToolCalls
	}
	if lastMC != len(modelCompleted) || lastTC != len(toolCompleted) {
		t.Fatalf("final budget.consumed = (%d,%d), want (%d,%d)", lastMC, lastTC, len(modelCompleted), len(toolCompleted))
	}
}

// 纯文本 run:无 tool 边界 → 仅 1 条 budget.consumed (model_calls=1, tool_calls=0)
func TestBudgetConsumedTextOnlyRun(t *testing.T) {
	h := testutil.New(t)
	h.Model.EnqueueText("输出风险列表:N+1 查询(证据:文件),每条建议有文件或代码证据。")
	ses := h.CreateSession("s")
	id, code, _ := h.CreateRun(ses, fixturePayload(t, ses), "bc-text-1")
	if code != 201 {
		t.Fatalf("create run: %d", code)
	}
	run := h.WaitTerminal(id, 30*time.Second)
	if run.Status != domain.RunSucceeded {
		t.Fatalf("want succeeded, got %s (%s)", run.Status, run.ErrorSummary)
	}
	all, err := h.Events.ListAfter(context.Background(), id, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	var consumed []domain.AgentEvent
	for _, ev := range all {
		if ev.Type == domain.EvtBudgetConsumed {
			consumed = append(consumed, ev)
		}
	}
	if len(consumed) != 1 {
		t.Fatalf("budget.consumed count = %d, want 1", len(consumed))
	}
	var p struct {
		ModelCalls int `json:"model_calls"`
		ToolCalls  int `json:"tool_calls"`
	}
	if err := json.Unmarshal([]byte(consumed[0].PayloadJSON), &p); err != nil {
		t.Fatal(err)
	}
	if p.ModelCalls != 1 || p.ToolCalls != 0 {
		t.Fatalf("payload = (%d,%d), want (1,0)", p.ModelCalls, p.ToolCalls)
	}
}
