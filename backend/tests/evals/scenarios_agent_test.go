package evals

import (
	"context"
	"errors"
	"testing"
	"time"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/tool"
	"rivus-agent-backend/tests/testutil"

	"github.com/cloudwego/eino/schema"
)

// 9) react 证据成功
func TestScenarioReactEvidenceSuccess(t *testing.T) {
	Run(t, Scenario{
		Name:            "react_evidence_success",
		Category:        "agent",
		Goal:            "分析并输出风险列表",
		SuccessCriteria: []string{"输出风险列表", "文件或代码证据"},
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueToolCall("tc-ev", "knowledge_search", `{"query":"重构"}`)
			h.Model.EnqueueText("输出风险列表：N+1 查询，每条建议有文件或代码证据。")
			return "eval-react-ok"
		},
		Want: Expect{
			TerminalStatus: domain.RunSucceeded,
			ResultContains: []string{`"passed":true`},
			RequiredEvents: []domain.EventType{domain.EvtVerifyPassed},
			MaxModelCalls:  2, MaxToolCalls: 1,
		},
	})
}

// 10) plan_execute 产生步骤
func TestScenarioPlanExecuteSteps(t *testing.T) {
	Run(t, Scenario{
		Name:            "plan_execute_steps",
		Category:        "agent",
		Goal:            "分阶段完成多步骤计划任务",
		SuccessCriteria: []string{"输出风险列表"},
		Mode:            "plan_execute",
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueToolCall("tc-p", "knowledge_search", `{"query":"x"}`)
			h.Model.EnqueueText("输出风险列表：分阶段结论，有文件或代码证据。")
			return "eval-plan"
		},
		Want: Expect{
			TerminalStatus: domain.RunSucceeded,
			MaxModelCalls:  4, MaxToolCalls: 1,
		},
		PostCheck: func(t *testing.T, h *testutil.Harness, runID string, _ domain.Run) {
			_, steps := h.GetRun(runID)
			if len(steps) == 0 {
				t.Fatal("plan_execute must create steps")
			}
		},
	})
}

// 11) 预算上限终止（稳定性）
func TestScenarioBudgetCapTerminates(t *testing.T) {
	Run(t, Scenario{
		Name:     "budget_cap_terminates",
		Category: "agent",
		Goal:     "loop forever",
		Budget: map[string]any{
			"max_tool_calls": 1, "max_model_calls": 10,
			"max_iterations": 6, "max_duration_seconds": 60,
		},
		Script: func(h *testutil.Harness) string {
			h.Model.SetFallback(loopTool("tc-l", "knowledge_search", `{"query":"loop"}`))
			return "eval-budget"
		},
		Want: Expect{
			TerminalStatus: domain.RunFailed,
			ErrorCode:      "tool_calls_budget_exceeded",
			RequiredEvents: []domain.EventType{domain.EvtBudgetExceeded},
		},
	})
}

// 12) 子 agent 失败被父 run 吸收（稳定性）
func TestScenarioSubagentChildFailureContained(t *testing.T) {
	Run(t, Scenario{
		Name:            "subagent_child_failure_contained",
		Category:        "agent",
		Goal:            "委派一个会失败的任务",
		SuccessCriteria: []string{"父任务收尾"},
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueToolCall("tc-t", "task", `{"goal":"boom"}`)
			h.Model.EnqueueFunc(func(int, []*schema.Message) (*schema.Message, error) {
				return nil, errors.New("child model boom") // 子 run 模型错误 → model_failed
			})
			h.Model.EnqueueText("父任务收尾，有文件或代码证据") // 父 run 收尾
			return "eval-child-fail"
		},
		Want: Expect{
			TerminalStatus: domain.RunSucceeded,
			MaxModelCalls:  3, MaxToolCalls: 1,
		},
		PostCheck: func(t *testing.T, h *testutil.Harness, runID string, _ domain.Run) {
			children, err := h.Tasks.ListChildrenByRun(context.Background(), runID)
			if err != nil || len(children) != 1 {
				t.Fatalf("children: %v len=%d", err, len(children))
			}
			child := h.WaitTerminal(children[0].ID, 30*time.Second)
			if child.Status != domain.RunFailed {
				t.Fatalf("child status=%s want failed", child.Status)
			}
		},
	})
}

// 13) 事件序列完整性（seq 单调 + 关键事件顺序）
func TestScenarioEventSequenceIntegrity(t *testing.T) {
	Run(t, Scenario{
		Name:            "event_sequence_integrity",
		Category:        "agent",
		Goal:            "输出风险列表",
		SuccessCriteria: []string{"输出风险列表"},
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueToolCall("tc-s", "knowledge_search", `{"query":"q"}`)
			h.Model.EnqueueText("输出风险列表：结论，有文件或代码证据。")
			return "eval-seq"
		},
		Want: Expect{
			TerminalStatus: domain.RunSucceeded,
			RequiredEvents: []domain.EventType{
				domain.EvtRunStarted, domain.EvtModelCompleted, domain.EvtToolCompleted, domain.EvtRunFinished,
			},
			MaxModelCalls: 2, MaxToolCalls: 1,
		},
		PostCheck: func(t *testing.T, h *testutil.Harness, runID string, _ domain.Run) {
			evs, err := h.Events.ListAfter(context.Background(), runID, 0, 500)
			if err != nil {
				t.Fatal(err)
			}
			last := int64(-1)
			idx := map[domain.EventType]int{}
			for i, ev := range evs {
				if ev.Seq <= last {
					t.Fatalf("seq not monotonic at %d: %d <= %d", i, ev.Seq, last)
				}
				last = ev.Seq
				if _, dup := idx[ev.Type]; !dup {
					idx[ev.Type] = i
				}
			}
			if idx[domain.EvtRunStarted] >= idx[domain.EvtRunFinished] {
				t.Fatal("run.started must precede run.finished")
			}
		},
	})
}

// 14) 超时终止（稳定性，~1s 真实等待）
func TestScenarioTimeoutTerminates(t *testing.T) {
	Run(t, Scenario{
		Name:     "timeout_terminates",
		Category: "agent",
		Goal:     "loop until timeout",
		Budget: map[string]any{
			"max_duration_seconds": 1, "max_tool_calls": 100000,
			"max_model_calls": 100000, "max_iterations": 100000,
		},
		Script: func(h *testutil.Harness) string {
			// 服务端会钳制 max_iterations（~12 轮即结束），故让每轮模型调用变慢，
			// 使 1s 的 max_duration_seconds 先行触发超时。
			h.Model.SetFallback(loopToolSlow("tc-to", "knowledge_search", `{"query":"loop"}`, 250*time.Millisecond))
			return "eval-timeout"
		},
		Want: Expect{
			TerminalStatus: domain.RunTimedOut,
			RequiredEvents: []domain.EventType{domain.EvtRunTimedOut},
		},
	})
}

// 15) invoke_skill 未知技能拒绝（确定性；spec 原方案 settings 禁用链路未验证，见计划头偏差说明）
func TestScenarioInvokeSkillUnknownFails(t *testing.T) {
	Run(t, Scenario{
		Name:            "invoke_skill_unknown_fails",
		Category:        "agent",
		Goal:            "加载不存在的 skill",
		SuccessCriteria: []string{"skill 加载"},
		NewHarness: func(t testutil.TB, _ ...tool.Definition) *testutil.Harness {
			return testutil.NewToolConfigHarness(t)
		},
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueToolCall("tc-uk", "invoke_skill", `{"name":"nope"}`)
			h.Model.EnqueueText("skill 加载失败")
			return "eval-skill-unknown"
		},
		Want: Expect{
			TerminalStatus: domain.RunFailed,
			RequiredEvents: []domain.EventType{domain.EvtToolFailed},
		},
	})
}
