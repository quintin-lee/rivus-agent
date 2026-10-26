package evals

import (
	"testing"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/tests/testutil"
)

// 冒烟场景：file_search 命中临时工作区文件。
func TestScenarioFileSearchHit(t *testing.T) {
	ws := withTempWorkspace(t)
	if err := writeFileRel(ws, "a.txt", "target needle here"); err != nil {
		t.Fatal(err)
	}
	Run(t, Scenario{
		Name:            "file_search_hit",
		Category:        "tool",
		Goal:            "在工作区中检索 needle",
		SuccessCriteria: []string{"找到 needle"},
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueToolCall("tc-fs", "file_search", `{"query":"needle"}`)
			h.Model.EnqueueText("已在 a.txt 中找到 needle")
			return "eval-file-search-hit"
		},
		Want: Expect{
			TerminalStatus: domain.RunSucceeded,
			RequiredEvents: []domain.EventType{domain.EvtToolCompleted},
			MaxModelCalls:  2,
			MaxToolCalls:   1,
		},
	})
}
