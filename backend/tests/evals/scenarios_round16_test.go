package evals

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/tests/testutil"
)

// 场景 28：同 Idempotency-Key 重放 POST /runs → 201 duplicated:true，run_id 相同且不重复执行。
func TestScenarioRunIdempotencyDuplicate(t *testing.T) {
	Run(t, Scenario{
		Name: "run_idempotency_duplicate", Category: "tool",
		Goal: "replaying a run create with the same idempotency key must not re-execute",
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueText("done")
			return "eval-r16-idem" // 固定幂等键，PostCheck 里重放
		},
		Want: Expect{TerminalStatus: domain.RunSucceeded, MaxModelCalls: 1},
		PostCheck: func(t *testing.T, h *testutil.Harness, runID string, run domain.Run) {
			payload := map[string]any{"session_id": run.SessionID, "goal": "idem-replay", "mode": "react"}
			code, body := h.Do("POST", "/api/v1/runs", payload,
				map[string]string{"Idempotency-Key": "eval-r16-idem"})
			if code != 201 {
				t.Fatalf("replay code = %d body = %s", code, body)
			}
			var out struct {
				RunID      string `json:"run_id"`
				Duplicated bool   `json:"duplicated"`
			}
			if err := json.Unmarshal(body, &out); err != nil {
				t.Fatal(err)
			}
			if !out.Duplicated || out.RunID != runID {
				t.Fatalf("replay duplicated=%v run_id=%q, want true/%q", out.Duplicated, out.RunID, runID)
			}
			// 模型只应被调用一次（重放不执行）。
			if calls := h.Model.Calls(); calls != 1 {
				t.Fatalf("model calls = %d, want 1 (replay must not re-execute)", calls)
			}
		},
	})
}

// 场景 29：tasks 批量上限（>5）被拒绝 —— handler 返回字符串结果（工具"成功"），无子 run。
func TestScenarioTasksBatchLimitRejected(t *testing.T) {
	Run(t, Scenario{
		Name: "tasks_batch_limit_rejected", Category: "tool",
		Goal: "a tasks batch larger than five must be rejected without creating children",
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueToolCall("tc1", "tasks",
				`{"tasks":[{"goal":"g1"},{"goal":"g2"},{"goal":"g3"},{"goal":"g4"},{"goal":"g5"},{"goal":"g6"}]}`)
			h.Model.SetFallback(func(_ int, msgs []*schema.Message) (*schema.Message, error) {
				hasTool := false
				for _, m := range msgs {
					if m.Role == schema.Tool {
						hasTool = true
					}
				}
				if !hasTool {
					return r14ToolCall("tc1", "tasks",
						`{"tasks":[{"goal":"g1"},{"goal":"g2"},{"goal":"g3"},{"goal":"g4"},{"goal":"g5"},{"goal":"g6"}]}`), nil
				}
				return &schema.Message{Role: schema.Assistant, Content: "batch rejected"}, nil
			})
			return ""
		},
		Want: Expect{TerminalStatus: domain.RunSucceeded,
			RequiredEvents: []domain.EventType{domain.EvtToolCompleted}},
		PostCheck: func(t *testing.T, h *testutil.Harness, runID string, run domain.Run) {
			ev, ok := findEvent(t, h, runID, domain.EvtToolCompleted)
			if !ok {
				t.Fatal("no tool.completed for tasks")
			}
			if !strings.Contains(ev.PayloadJSON, "batch size must be 1..5") {
				t.Fatalf("rejection message not in payload: %s", ev.PayloadJSON)
			}
			children, err := h.Tasks.ListChildrenByRun(context.Background(), runID)
			if err != nil {
				t.Fatal(err)
			}
			if len(children) != 0 {
				t.Fatalf("children = %d, want 0 (batch must be rejected before spawning)", len(children))
			}
		},
	})
}

// 场景 30：审批重复决策 → 409 conflict（已决策的审批不可再次 POST）。
func TestScenarioApprovalDoubleDecideConflict(t *testing.T) {
	root := withTempWorkspace(t)
	if err := writeFileRel(root, "a.txt", "hello\n"); err != nil {
		t.Fatal(err)
	}
	Run(t, Scenario{
		Name: "approval_double_decide_conflict", Category: "tool",
		Goal: "deciding an already-decided approval must return 409",
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueToolCall("tc1", "file_edit",
				`{"path":"a.txt","old_text":"hello","new_text":"world"}`)
			h.Model.SetFallback(func(_ int, msgs []*schema.Message) (*schema.Message, error) {
				hasTool := false
				for _, m := range msgs {
					if m.Role == schema.Tool {
						hasTool = true
					}
				}
				if !hasTool { // 审批后的新执行：重发 file_edit
					return r14ToolCall("tc2", "file_edit",
						`{"path":"a.txt","old_text":"hello","new_text":"world"}`), nil
				}
				return &schema.Message{Role: schema.Assistant, Content: "edited"}, nil
			})
			return ""
		},
		MidRun: func(t *testing.T, h *testutil.Harness, runID string) {
			approveFirst(t, h, runID)
		},
		Want: Expect{TerminalStatus: domain.RunSucceeded},
		PostCheck: func(t *testing.T, h *testutil.Harness, runID string, run domain.Run) {
			aID := firstApprovalID(t, h, runID)
			code, body := h.Do("POST", "/api/v1/runs/"+runID+"/approvals/"+aID,
				map[string]any{"approve": false, "reason": "second decide"}, nil)
			if code != 409 {
				t.Fatalf("second decide code = %d body = %s, want 409 conflict", code, body)
			}
			if !strings.Contains(string(body), "conflict") {
				t.Fatalf("body missing conflict: %s", body)
			}
		},
	})
}
