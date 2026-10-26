package runtime

import (
	"testing"

	"rivus-agent-backend/internal/domain"
)

func TestOverBudgetZeroLimitsNeverTrip(t *testing.T) {
	tr := NewBudgetTracker(domain.Budget{})
	tr.AddModelCall()
	tr.AddToolCall()
	tr.AddOutput(1 << 20)
	if code, hit := tr.OverBudget(); hit {
		t.Fatalf("zero limits hit with code %q; want no hit", code)
	}
}

func TestOverBudgetDimensions(t *testing.T) {
	tests := []struct {
		name     string
		limit    domain.Budget
		use      func(tr *BudgetTracker)
		wantHit  bool
		wantCode string
	}{
		{"tool below", domain.Budget{MaxToolCalls: 2}, func(tr *BudgetTracker) { tr.AddToolCall() }, false, ""},
		{"tool at", domain.Budget{MaxToolCalls: 2}, func(tr *BudgetTracker) { tr.AddToolCall(); tr.AddToolCall() }, true, "tool_calls_budget_exceeded"},
		{"model below", domain.Budget{MaxModelCalls: 2}, func(tr *BudgetTracker) { tr.AddModelCall() }, false, ""},
		{"model at", domain.Budget{MaxModelCalls: 1}, func(tr *BudgetTracker) { tr.AddModelCall() }, true, "model_calls_budget_exceeded"},
		{"output below", domain.Budget{MaxOutputBytes: 10}, func(tr *BudgetTracker) { tr.AddOutput(9) }, false, ""},
		{"output at", domain.Budget{MaxOutputBytes: 10}, func(tr *BudgetTracker) { tr.AddOutput(10) }, true, "output_bytes_budget_exceeded"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := NewBudgetTracker(tt.limit)
			tt.use(tr)
			code, hit := tr.OverBudget()
			if hit != tt.wantHit || code != tt.wantCode {
				t.Fatalf("OverBudget() = (%q, %v); want (%q, %v)", code, hit, tt.wantCode, tt.wantHit)
			}
		})
	}
}

// 触限优先级固定：tool > model > output。
func TestOverBudgetPriorityToolFirst(t *testing.T) {
	tr := NewBudgetTracker(domain.Budget{MaxToolCalls: 1, MaxModelCalls: 1, MaxOutputBytes: 1})
	tr.AddToolCall()
	tr.AddModelCall()
	tr.AddOutput(1)
	code, hit := tr.OverBudget()
	if !hit || code != "tool_calls_budget_exceeded" {
		t.Fatalf("OverBudget() = (%q, %v); want tool_calls_budget_exceeded", code, hit)
	}
}
