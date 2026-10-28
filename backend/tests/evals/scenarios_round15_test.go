package evals

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/tests/testutil"
)

// 场景 25：run 详情三端点（R14 RunDetailDialog 的后端契约）。
func TestScenarioRunDetailAPI(t *testing.T) {
	Run(t, Scenario{
		Name: "run_detail_api", Category: "tool",
		Goal: "exercise the run detail endpoints after a successful run",
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueText("all done")
			return ""
		},
		Want: Expect{TerminalStatus: domain.RunSucceeded, MaxModelCalls: 1},
		PostCheck: func(t *testing.T, h *testutil.Harness, runID string, run domain.Run) {
			code, body := h.Do(http.MethodGet, "/api/v1/runs/"+runID, nil, nil)
			if code != 200 {
				t.Fatalf("get run: %d %s", code, body)
			}
			if !jsonContains(body, `"id":"`+runID+`"`) {
				t.Fatalf("run body missing id %s: %s", runID, body)
			}
			code, body = h.Do(http.MethodGet, "/api/v1/runs/"+runID+"/events?after=0", nil, nil)
			if code != 200 {
				t.Fatalf("get events: %d %s", code, body)
			}
			if !jsonContains(body, string(domain.EvtRunStarted)) {
				t.Fatalf("events missing run.started: %s", body)
			}
			code, body = h.Do(http.MethodGet, "/api/v1/runs/"+runID+"/approvals", nil, nil)
			if code != 200 {
				t.Fatalf("get approvals: %d %s", code, body)
			}
			if !jsonContains(body, `"approvals"`) {
				t.Fatalf("approvals body malformed: %s", body)
			}
		},
	})
}

// 场景 26：GET /runs?status= 过滤（批2 特性端到端）。
func TestScenarioRunsStatusFilter(t *testing.T) {
	Run(t, Scenario{
		Name: "runs_status_filter", Category: "agent",
		Goal: "one failed run and one succeeded run, filter by status",
		Budget: map[string]any{
			"max_duration_seconds": 30, "max_model_calls": 10, "max_tool_calls": 10,
			"max_iterations": 10, "max_output_bytes": 10,
		},
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueText("this single output is definitely longer than ten bytes")
			return ""
		},
		Want: Expect{TerminalStatus: domain.RunFailed, ErrorCode: "output_bytes_budget_exceeded"},
		PostCheck: func(t *testing.T, h *testutil.Harness, runID string, run domain.Run) {
			// 同会话再造一个成功 run（FIFO：先排队文本再创建）
			h.Model.EnqueueText("fine")
			id2, ccode, _ := h.CreateRun(run.SessionID, map[string]any{"goal": "filter-ok"}, "r15-filter-ok")
			if ccode != 201 {
				t.Fatalf("create run2: %d", ccode)
			}
			if got := h.WaitTerminal(id2, 30*time.Second); got.Status != domain.RunSucceeded {
				t.Fatalf("run2 = %s, want succeeded", got.Status)
			}
			assertOnlyRun(t, h, "failed", runID)
			assertOnlyRun(t, h, "succeeded", id2)
			// 无过滤：两者都在
			code2, body2 := h.Do(http.MethodGet, "/api/v1/runs?limit=50", nil, nil)
			if code2 != 200 {
				t.Fatalf("unfiltered: %d", code2)
			}
			ids := runIDs(body2)
			if !containsID(ids, runID) || !containsID(ids, id2) {
				t.Fatalf("unfiltered missing runs: %v want {%s,%s}", ids, runID, id2)
			}
		},
	})
}

// 场景 27：审批拒绝 → failed + approval_rejected，工具未执行。
func TestScenarioApprovalRejectedFlow(t *testing.T) {
	root := withTempWorkspace(t)
	target := filepath.Join(root, "reject.txt")
	Run(t, Scenario{
		Name: "approval_rejected_flow", Category: "tool",
		Goal: "reject the file_edit approval and observe terminal failure",
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueToolCall("tc1", "file_edit",
				`{"path":"reject.txt","old_text":"hello","new_text":"world"}`)
			return ""
		},
		MidRun: func(t *testing.T, h *testutil.Harness, runID string) {
			aID := firstApprovalID(t, h, runID)
			code, body := h.Do("POST", "/api/v1/runs/"+runID+"/approvals/"+aID,
				map[string]any{"approve": false, "reason": "eval-reject"}, nil)
			if code != 200 {
				t.Fatalf("reject: %d %s", code, body)
			}
		},
		Want: Expect{TerminalStatus: domain.RunFailed, ErrorCode: "approval_rejected"},
		PostCheck: func(t *testing.T, h *testutil.Harness, runID string, run domain.Run) {
			if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatalf("file must not be created, err = %v", err)
			}
			if _, ok := findEvent(t, h, runID, domain.EvtToolCompleted); ok {
				t.Fatal("tool.completed must not exist after rejection")
			}
			ev, ok := findEvent(t, h, runID, domain.EvtApprovalDecided)
			if !ok || !jsonContains([]byte(ev.PayloadJSON), `"approved":false`) {
				t.Fatalf("approval.decided payload = %v %s, want approved:false", ok, ev.PayloadJSON)
			}
		},
	})
}

// ---- helpers ----

func jsonContains(body []byte, sub string) bool {
	return len(body) >= len(sub) && contains(string(body), sub)
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func runIDs(body []byte) []string {
	var out struct {
		Runs []domain.Run `json:"runs"`
	}
	_ = json.Unmarshal(body, &out)
	ids := make([]string, 0, len(out.Runs))
	for _, r := range out.Runs {
		ids = append(ids, r.ID)
	}
	return ids
}

func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func assertOnlyRun(t *testing.T, h *testutil.Harness, status, runID string) {
	t.Helper()
	code, body := h.Do(http.MethodGet, "/api/v1/runs?status="+status, nil, nil)
	if code != 200 {
		t.Fatalf("filtered %s: %d", status, code)
	}
	ids := runIDs(body)
	if len(ids) != 1 || !containsID(ids, runID) {
		t.Fatalf("status=%s got %v, want [%s]", status, ids, runID)
	}
}
