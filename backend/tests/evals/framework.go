// Package evals 提供基于 mock 模型的确定性评估：场景目录 + 三维度判定 + JSON 报告。
package evals

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/tool"
	"rivus-agent-backend/tests/testutil"
)

// Expect 定义场景的三维度期望。
type Expect struct {
	TerminalStatus domain.RunStatus   // 期望终态
	ErrorCode      string             // 精确匹配；空=不检查
	ResultContains []string           // ResultJSON 子串全命中
	MaxModelCalls  int                // 效率维上限（0=不检查）
	MaxToolCalls   int
	RequiredEvents []domain.EventType // 正确性维：事件存在性
}

// Scenario 描述一个评估场景。
type Scenario struct {
	Name, Category  string // category: "tool" | "agent"
	Goal            string
	SuccessCriteria []string
	Mode            string         // 空 = react
	Budget          map[string]any // 覆盖默认预算（可空）

	// Script 在 run 创建前排队 mock 模型输出；返回幂等键（空则自动生成）。
	Script func(h *testutil.Harness) string
	// MidRun 可选：WaitTerminal 返回 waiting_approval 后调用（如批准），随后二次等待终态。
	MidRun func(t *testing.T, h *testutil.Harness, runID string)
	// PostCheck 可选：终态后的额外断言（事件载荷、子 run、文件系统等）。
	PostCheck func(t *testing.T, h *testutil.Harness, runID string, run domain.Run)
	// NewHarness 可选：自定义 harness（默认 testutil.New）。
	NewHarness func(t testutil.TB, extraTools ...tool.Definition) *testutil.Harness

	Want Expect
}

// Result 是一个场景的评估结果（同时是报告行）。
type Result struct {
	Name       string `json:"name"`
	Category   string `json:"category"`
	Correct    bool   `json:"correct"`
	Efficient  bool   `json:"efficient"`
	Stable     bool   `json:"stable"`
	Pass       bool   `json:"pass"`
	Detail     string `json:"detail"`
	ModelCalls int    `json:"model_calls"`
	ToolCalls  int    `json:"tool_calls"`
	DurationMs int64  `json:"duration_ms"`
}

var (
	resultsMu sync.Mutex
	results   []Result
)

func record(r Result) {
	resultsMu.Lock()
	defer resultsMu.Unlock()
	results = append(results, r)
}

// Run 执行一个场景并做三维度判定；任一维度失败即 t.Errorf（CI 门禁）。
func Run(t *testing.T, s Scenario) Result {
	t.Helper()
	start := time.Now()

	newHarness := s.NewHarness
	if newHarness == nil {
		newHarness = testutil.New
	}
	h := newHarness(t)

	ses := h.CreateSession("eval-" + s.Name)
	idem := s.Script(h)
	if idem == "" {
		idem = "eval-" + s.Name
	}
	payload := map[string]any{"goal": s.Goal}
	if len(s.SuccessCriteria) > 0 {
		payload["success_criteria"] = s.SuccessCriteria
	}
	if s.Mode != "" {
		payload["mode"] = s.Mode
	}
	if s.Budget != nil {
		payload["budget"] = s.Budget
	}
	id, _, _ := h.CreateRun(ses, payload, idem)
	run := h.WaitTerminal(id, 30*time.Second)
	if s.MidRun != nil && run.Status == domain.RunWaitingApproval {
		s.MidRun(t, h, id)
		run = h.WaitTerminal(id, 30*time.Second)
	}

	evs, evErr := h.Events.ListAfter(context.Background(), id, 0, 500)
	toolCalls := 0
	eventSet := map[domain.EventType]bool{}
	if evErr == nil {
		for _, ev := range evs {
			eventSet[ev.Type] = true
			if ev.Type == domain.EvtToolCompleted {
				toolCalls++
			}
		}
	}
	modelCalls := h.Model.Calls()

	var problems []string
	// 正确性
	if run.Status != s.Want.TerminalStatus {
		problems = append(problems, fmt.Sprintf("status=%s want %s", run.Status, s.Want.TerminalStatus))
	}
	if s.Want.ErrorCode != "" && run.ErrorCode != s.Want.ErrorCode {
		problems = append(problems, fmt.Sprintf("error_code=%q want %q", run.ErrorCode, s.Want.ErrorCode))
	}
	for _, sub := range s.Want.ResultContains {
		if !strings.Contains(run.ResultJSON, sub) {
			problems = append(problems, fmt.Sprintf("result_json missing %q", sub))
		}
	}
	for _, evt := range s.Want.RequiredEvents {
		if !eventSet[evt] {
			problems = append(problems, fmt.Sprintf("missing event %s", evt))
		}
	}
	correct := len(problems) == 0
	// 效率
	effProblems := 0
	if s.Want.MaxModelCalls > 0 && modelCalls > s.Want.MaxModelCalls {
		problems = append(problems, fmt.Sprintf("model_calls=%d > %d", modelCalls, s.Want.MaxModelCalls))
		effProblems++
	}
	if s.Want.MaxToolCalls > 0 && toolCalls > s.Want.MaxToolCalls {
		problems = append(problems, fmt.Sprintf("tool_calls=%d > %d", toolCalls, s.Want.MaxToolCalls))
		effProblems++
	}
	efficient := effProblems == 0
	// 稳定性：在时限内到达终态
	stable := run.Status.Terminal()
	if !stable {
		problems = append(problems, fmt.Sprintf("not terminal within timeout: %s", run.Status))
	}

	if s.PostCheck != nil {
		s.PostCheck(t, h, id, run)
	}

	r := Result{
		Name: s.Name, Category: s.Category,
		Correct: correct, Efficient: efficient, Stable: stable,
		Pass:       correct && efficient && stable,
		Detail:     strings.Join(problems, "; "),
		ModelCalls: modelCalls, ToolCalls: toolCalls,
		DurationMs: time.Since(start).Milliseconds(),
	}
	record(r)
	if !r.Pass {
		t.Errorf("scenario %s FAILED: correct=%v efficient=%v stable=%v detail=%s", r.Name, r.Correct, r.Efficient, r.Stable, r.Detail)
	}
	return r
}
