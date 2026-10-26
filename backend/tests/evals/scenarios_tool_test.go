package evals

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/tool"
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

// 1) file_read 内容读回（写路径由 file_edit 审批场景覆盖）
func TestScenarioFileReadContent(t *testing.T) {
	ws := withTempWorkspace(t)
	if err := writeFileRel(ws, "notes/x.md", "ROUNDTRIP 内容 123"); err != nil {
		t.Fatal(err)
	}
	Run(t, Scenario{
		Name:            "file_read_content",
		Category:        "tool",
		Goal:            "读取 notes/x.md 内容",
		SuccessCriteria: []string{"读取完成"},
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueToolCall("tc-rd", "file_read", `{"path":"notes/x.md"}`)
			h.Model.EnqueueText("读取完成 ROUNDTRIP 内容 123")
			return "eval-file-read"
		},
		Want: Expect{
			TerminalStatus: domain.RunSucceeded,
			MaxModelCalls:  2, MaxToolCalls: 1,
		},
	})
}

// 3) file_edit 审批流：等待审批 → 批准 → 成功 + 文件变更
func TestScenarioFileEditApprovalFlow(t *testing.T) {
	ws := withTempWorkspace(t)
	if err := writeFileRel(ws, "svc/a.go", "old line\n"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(ws, "svc", "a.go")
	Run(t, Scenario{
		Name:            "file_edit_approval_flow",
		Category:        "tool",
		Goal:            "修改 svc/a.go",
		SuccessCriteria: []string{"修改完成"},
		Script: func(h *testutil.Harness) string {
			h.Model.SetFallback(reissueToolThenText("tc-ed", "file_edit",
				`{"path":"svc/a.go","old_text":"old line","new_text":"new line"}`,
				"修改完成 new line"))
			return "eval-file-edit"
		},
		Want: Expect{
			TerminalStatus: domain.RunSucceeded,
			RequiredEvents: []domain.EventType{
				domain.EvtApprovalRequested, domain.EvtApprovalDecided, domain.EvtToolCompleted,
			},
		},
		MidRun: func(t *testing.T, h *testutil.Harness, runID string) {
			aID := firstApprovalID(t, h, runID)
			code, body := h.Do("POST", "/api/v1/runs/"+runID+"/approvals/"+aID,
				map[string]any{"approve": true, "reason": "eval"}, nil)
			if code != 200 {
				t.Fatalf("approve: %d %s", code, body)
			}
		},
		PostCheck: func(t *testing.T, _ *testutil.Harness, _ string, _ domain.Run) {
			b, err := os.ReadFile(path)
			if err != nil || !strings.Contains(string(b), "new line") {
				t.Fatalf("file not edited: %v %s", err, b)
			}
		},
	})
}

// 4) http_fetch 空 allowlist → 批准后处理器拒绝（external 工具先审批再执行）
func TestScenarioHTTPFetchAllowlistDeny(t *testing.T) {
	disableHTTPFetch(t)
	Run(t, Scenario{
		Name:            "http_fetch_allowlist_deny",
		Category:        "tool",
		Goal:            "抓取外部页面",
		SuccessCriteria: []string{"抓取"},
		Script: func(h *testutil.Harness) string {
			h.Model.SetFallback(reissueToolThenText("tc-hf", "http_fetch",
				`{"url":"http://example.com"}`, "抓取被禁用"))
			return "eval-http-deny"
		},
		Want: Expect{
			TerminalStatus: domain.RunFailed,
			RequiredEvents: []domain.EventType{domain.EvtToolFailed},
		},
		MidRun: func(t *testing.T, h *testutil.Harness, runID string) {
			aID := firstApprovalID(t, h, runID)
			code, body := h.Do("POST", "/api/v1/runs/"+runID+"/approvals/"+aID,
				map[string]any{"approve": true, "reason": "eval"}, nil)
			if code != 200 {
				t.Fatalf("approve: %d %s", code, body)
			}
		},
	})
}

// 5) knowledge_search 命中临时知识库
func TestScenarioKnowledgeSearchHit(t *testing.T) {
	kd := withTempKnowledge(t)
	if err := writeFileRel(kd, "perf.md", "性能问题：N+1 查询"); err != nil {
		t.Fatal(err)
	}
	Run(t, Scenario{
		Name:            "knowledge_search_hit",
		Category:        "tool",
		Goal:            "检索 N+1 问题",
		SuccessCriteria: []string{"N+1"},
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueToolCall("tc-ks", "knowledge_search", `{"query":"N+1"}`)
			h.Model.EnqueueText("发现 N+1 查询问题")
			return "eval-knowledge"
		},
		Want: Expect{
			TerminalStatus: domain.RunSucceeded,
			MaxModelCalls:  2, MaxToolCalls: 1,
		},
	})
}

// 6) memory 跨 run 往返
func TestScenarioMemoryRoundtrip(t *testing.T) {
	Run(t, Scenario{
		Name:            "memory_roundtrip",
		Category:        "tool",
		Goal:            "记录项目偏好",
		SuccessCriteria: []string{"已记录偏好"},
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueToolCall("tc-mw", "memory_write", `{"content":"项目偏好 Go"}`)
			h.Model.EnqueueText("已记录偏好")
			return "eval-memory-1"
		},
		Want: Expect{
			TerminalStatus: domain.RunSucceeded,
			MaxModelCalls:  2, MaxToolCalls: 1,
		},
		PostCheck: func(t *testing.T, h *testutil.Harness, runID string, _ domain.Run) {
			r1, _ := h.GetRun(runID)
			ses := r1.SessionID
			code, body := h.Do("GET", "/api/v1/sessions/"+ses+"/memories", nil, nil)
			if code != 200 || !strings.Contains(string(body), "项目偏好 Go") {
				t.Fatalf("memories API: %d %s", code, body)
			}
			h.Model.EnqueueToolCall("tc-mr", "memory_read", `{"query":"偏好"}`)
			h.Model.EnqueueText("根据记忆：项目偏好 Go")
			id2, _, _ := h.CreateRun(ses, map[string]any{"goal": "复述记忆"}, "eval-memory-2")
			run2 := h.WaitTerminal(id2, 30*time.Second)
			if run2.Status != domain.RunSucceeded {
				t.Fatalf("run2 status=%s", run2.Status)
			}
			if _, ok := findEvent(t, h, id2, domain.EvtToolCompleted); !ok {
				t.Fatal("run2 memory_read tool.completed missing")
			}
		},
	})
}

// 7) invoke_skill 加载 demo skill 正文（invoke_skill 仅在 tool-config harness 注册）
func TestScenarioInvokeSkillLoad(t *testing.T) {
	Run(t, Scenario{
		Name:            "invoke_skill_load",
		Category:        "tool",
		Goal:            "加载 demo skill",
		SuccessCriteria: []string{"demo skill"},
		NewHarness: func(t testutil.TB, _ ...tool.Definition) *testutil.Harness {
			return testutil.NewToolConfigHarness(t)
		},
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueToolCall("tc-sk", "invoke_skill", `{"name":"demo"}`)
			h.Model.EnqueueText("demo skill 已加载")
			return "eval-skill"
		},
		Want: Expect{
			TerminalStatus: domain.RunSucceeded,
			MaxModelCalls:  2, MaxToolCalls: 1,
		},
	})
}

// 8) tasks 批量：2 子任务、输入序、子 run 终态
func TestScenarioTasksBatchOrder(t *testing.T) {
	Run(t, Scenario{
		Name:            "tasks_batch_order",
		Category:        "tool",
		Goal:            "批量完成两个子任务",
		SuccessCriteria: []string{"两个子任务已完成"},
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueToolCall("tc-b", "tasks",
				`{"tasks":[{"goal":"g1"},{"goal":"g2"}],"max_concurrency":1}`)
			h.Model.EnqueueText("g1 完成")        // child1
			h.Model.EnqueueText("g2 完成")        // child2
			h.Model.EnqueueText("两个子任务已完成") // parent
			return "eval-tasks"
		},
		Want: Expect{
			TerminalStatus: domain.RunSucceeded,
			RequiredEvents: []domain.EventType{domain.EvtToolCompleted},
			MaxModelCalls:  4, MaxToolCalls: 1,
		},
		PostCheck: func(t *testing.T, h *testutil.Harness, runID string, _ domain.Run) {
			children, err := h.Tasks.ListChildrenByRun(context.Background(), runID)
			if err != nil {
				t.Fatalf("list children: %v", err)
			}
			if len(children) != 2 {
				t.Fatalf("children=%d want 2", len(children))
			}
			for i, want := range []string{"g1", "g2"} {
				if children[i].Goal != want {
					t.Fatalf("children[%d].Goal=%q want %q", i, children[i].Goal, want)
				}
				if got := h.WaitTerminal(children[i].ID, 30*time.Second); !got.Status.Terminal() {
					t.Fatalf("child %d not terminal: %s", i, got.Status)
				}
			}
		},
	})
}
