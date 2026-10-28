package evals

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/tool"
	"rivus-agent-backend/tests/testutil"
)

// r14ToolCall 构造一条工具调用消息（场景脚本用）。
func r14ToolCall(id, name, args string) *schema.Message {
	return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
		ID: id, Type: "function",
		Function: schema.FunctionCall{Name: name, Arguments: args},
	}}}
}

// 场景 21：invoke_skill 加载 references/ 子文件（Skill 增强的评估覆盖）。
func TestScenarioInvokeSkillReferenceLoad(t *testing.T) {
	Run(t, Scenario{
		Name: "invoke_skill_reference_load", Category: "tool",
		Goal: "load the cheatsheet reference of the demo skill",
		NewHarness: func(t testutil.TB, _ ...tool.Definition) *testutil.Harness {
			return testutil.NewToolConfigHarness(t)
		},
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueToolCall("tc1", "invoke_skill", `{"name":"demo","reference":"cheatsheet.md"}`)
			h.Model.EnqueueText("loaded")
			return ""
		},
		Want: Expect{TerminalStatus: domain.RunSucceeded, MaxToolCalls: 1,
			RequiredEvents: []domain.EventType{domain.EvtToolCompleted}},
		PostCheck: func(t *testing.T, h *testutil.Harness, runID string, run domain.Run) {
			ev, ok := findEvent(t, h, runID, domain.EvtToolCompleted)
			if !ok {
				t.Fatal("no tool.completed")
			}
			if !strings.Contains(ev.PayloadJSON, "R14-CHEATSHEET-MARKER") {
				t.Fatalf("reference content not in payload: %s", ev.PayloadJSON)
			}
		},
	})
}

// 场景 22：file_edit（审批）后 file_read 回读，内容落盘一致。
func TestScenarioFileEditReadRoundtrip(t *testing.T) {
	root := withTempWorkspace(t)
	if err := writeFileRel(root, "a.txt", "hello\n"); err != nil {
		t.Fatal(err)
	}
	Run(t, Scenario{
		Name: "file_edit_read_roundtrip", Category: "tool",
		Goal: "edit a file with approval then read it back",
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueToolCall("tc1", "file_edit", `{"path":"a.txt","old_text":"hello","new_text":"world"}`)
			calls := 0
			h.Model.SetFallback(func(_ int, msgs []*schema.Message) (*schema.Message, error) {
				hasTool := false
				for _, m := range msgs {
					if m.Role == schema.Tool {
						hasTool = true
					}
				}
				calls++
				switch {
				case !hasTool: // 审批后的新执行：重发 file_edit
					return r14ToolCall("tc2", "file_edit", `{"path":"a.txt","old_text":"hello","new_text":"world"}`), nil
				case calls == 2: // file_edit 已执行：改发 file_read
					return r14ToolCall("tc3", "file_read", `{"path":"a.txt"}`), nil
				default: // file_read 已执行：收尾
					return &schema.Message{Role: schema.Assistant, Content: "edited"}, nil
				}
			})
			return ""
		},
		MidRun: func(t *testing.T, h *testutil.Harness, runID string) {
			approveFirst(t, h, runID)
		},
		Want: Expect{TerminalStatus: domain.RunSucceeded},
		PostCheck: func(t *testing.T, h *testutil.Harness, runID string, run domain.Run) {
			b, err := os.ReadFile(filepath.Join(root, "a.txt"))
			if err != nil || string(b) != "world\n" {
				t.Fatalf("file content = %q, err = %v", b, err)
			}
			if ev, ok := findEvent(t, h, runID, domain.EvtToolCompleted); ok &&
				!strings.Contains(ev.PayloadJSON, "world") {
				t.Fatalf("file_read output missing content: %s", ev.PayloadJSON)
			}
		},
	})
}

// 场景 23：output_bytes 预算维度终止运行（三个预算维度中最后补齐的一个）。
func TestScenarioBudgetOutputBytesCap(t *testing.T) {
	Run(t, Scenario{
		Name: "budget_output_bytes_cap", Category: "agent",
		Goal: "emit more output bytes than the budget allows",
		Budget: map[string]any{
			"max_duration_seconds": 30, "max_model_calls": 10, "max_tool_calls": 10,
			"max_iterations": 10, "max_output_bytes": 10,
		},
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueText("this single output is definitely longer than ten bytes")
			return ""
		},
		Want: Expect{TerminalStatus: domain.RunFailed, ErrorCode: "output_bytes_budget_exceeded",
			RequiredEvents: []domain.EventType{domain.EvtBudgetExceeded}},
	})
}

// 场景 24：运行中取消（POST /cancel），终态 cancelled。
func TestScenarioCancelRunningRun(t *testing.T) {
	Run(t, Scenario{
		Name: "cancel_running_run", Category: "agent",
		Goal: "eval-cancel-marker",
		Budget: map[string]any{
			"max_duration_seconds": 30, "max_model_calls": 10, "max_tool_calls": 10,
			"max_iterations": 10, "max_output_bytes": 50000,
		},
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueToolCall("tc1", "knowledge_search", `{"query":"loop"}`)
			h.Model.SetFallback(func(_ int, _ []*schema.Message) (*schema.Message, error) {
				// 第二轮模型调用时取消：owner-wide 列表（R9）按 goal marker 定位 run。
				code, body := h.Do(http.MethodGet, "/api/v1/runs?limit=50", nil, nil)
				if code != 200 {
					return nil, fmt.Errorf("list runs: code=%d body=%s", code, body)
				}
				var listed struct {
					Runs []domain.Run `json:"runs"`
				}
				if err := json.Unmarshal(body, &listed); err != nil {
					return nil, err
				}
				for _, r := range listed.Runs {
					if r.Goal == "eval-cancel-marker" {
						c, b := h.Do(http.MethodPost, "/api/v1/runs/"+r.ID+"/cancel",
							map[string]string{}, nil)
						if c != 200 {
							return nil, fmt.Errorf("cancel: %d %s", c, b)
						}
						break
					}
				}
				time.Sleep(100 * time.Millisecond) // 让取消先于收尾文本传播
				return &schema.Message{Role: schema.Assistant, Content: "cancelling"}, nil
			})
			return ""
		},
		Want: Expect{TerminalStatus: domain.RunCancelled},
		PostCheck: func(t *testing.T, h *testutil.Harness, runID string, run domain.Run) {
			// 取消路径的 run 会带 error_code "cancelled"（观测到的实际行为）。
			if run.ErrorCode != "cancelled" {
				t.Fatalf("cancelled run error_code = %q, want %q", run.ErrorCode, "cancelled")
			}
		},
	})
}
