package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/tool/builtin"
	"rivus-agent-backend/tests/testutil"
)

// withTempWorkspace 把 builtin 工作区/知识目录切到临时目录，测试结束恢复缺省。
// 包内测试串行（无 t.Parallel），全局切换安全。
func withTempWorkspace(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	builtin.SetDirs(ws, t.TempDir())
	t.Cleanup(func() { builtin.SetDirs("./workspace", "./knowledge") })
	return ws
}

func TestFileEditApprovalFlow(t *testing.T) {
	ws := withTempWorkspace(t)
	if err := os.MkdirAll(filepath.Join(ws, "drafts"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(ws, "drafts", "note.txt")
	if err := os.WriteFile(target, []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := testutil.New(t)
	h.Model.EnqueueToolCall("tc1", "file_edit", `{"path":"drafts/note.txt","old_text":"hello","new_text":"HELLO"}`)
	finalText := "输出风险列表：已编辑（证据：file_edit 结果），每条建议有文件证据。"
	// 审批后的 resume 是全新执行：无 Tool 角色消息时重新发起 file_edit
	// （此时已在 ApprovedTools 中，网关放行）；有 Tool 结果则收尾。
	h.Model.SetFallback(func(_ int, input []*schema.Message) (*schema.Message, error) {
		for _, m := range input {
			if m.Role == schema.Tool {
				return &schema.Message{Role: schema.Assistant, Content: finalText}, nil
			}
		}
		return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
			ID: "tc-retry", Type: "function",
			Function: schema.FunctionCall{Name: "file_edit", Arguments: `{"path":"drafts/note.txt","old_text":"hello","new_text":"HELLO"}`},
		}}}, nil
	})
	ses := h.CreateSession("s")
	id, code, _ := h.CreateRun(ses, fixturePayload(t, ses), "fedit-1")
	if code != 201 {
		t.Fatalf("create run: %d", code)
	}

	// file_edit 是 RiskWrite+ApprovalConditional → 默认走 waiting_approval
	run := h.WaitTerminal(id, 30*time.Second)
	if run.Status != domain.RunWaitingApproval {
		t.Fatalf("want waiting_approval, got %s (%s)", run.Status, run.ErrorSummary)
	}
	records, err := h.Approvals.ListByRun(context.Background(), id)
	if err != nil || len(records) != 1 {
		t.Fatalf("want 1 approval record, got %d err=%v", len(records), err)
	}
	if records[0].ToolName != "file_edit" {
		t.Fatalf("approval tool = %s, want file_edit", records[0].ToolName)
	}

	// 批准 → 从 Checkpoint 恢复执行 → 文件实际变更 → Run 成功
	code, body := h.Do("POST", "/api/v1/runs/"+id+"/approvals/"+records[0].ID,
		map[string]any{"approve": true, "reason": "ok"}, nil)
	if code != 200 {
		t.Fatalf("decide: %d %s", code, body)
	}
	run = h.WaitTerminal(id, 30*time.Second)
	if run.Status != domain.RunSucceeded {
		t.Fatalf("want succeeded after approval, got %s (%s)", run.Status, run.ErrorSummary)
	}
	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "HELLO world\n" {
		t.Fatalf("file not edited after approval: %q", string(b))
	}
}

func TestFileSearchInChildWhitelist(t *testing.T) {
	ws := withTempWorkspace(t)
	if err := os.WriteFile(filepath.Join(ws, "doc.md"), []byte("needles here\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := testutil.New(t)
	// 共享 FIFO：父 call1 = 调 task；子 call1 = file_search；子 call2 = 子 final；父 call2 = 父 final
	h.Model.EnqueueToolCall("tc-p", "task", `{"goal":"search the docs","tools":["file_search"]}`)
	h.Model.EnqueueToolCall("tc-c", "file_search", `{"query":"needles"}`)
	h.Model.EnqueueText("child answer with search evidence")
	h.Model.EnqueueText("parent final done")
	ses := h.CreateSession("s")
	id, code, _ := h.CreateRun(ses, fixturePayload(t, ses), "fsearch-1")
	if code != 201 {
		t.Fatalf("create run: %d", code)
	}
	run := h.WaitTerminal(id, 30*time.Second)
	if run.Status != domain.RunSucceeded {
		t.Fatalf("parent want succeeded got %s (%s)", run.Status, run.ErrorSummary)
	}
	children, err := h.Tasks.ListChildrenByRun(context.Background(), id)
	if err != nil || len(children) != 1 {
		t.Fatalf("want 1 child, got %d err=%v", len(children), err)
	}
	// 子事件流证明 file_search 真的被执行（tool.completed）
	evs, _ := h.Events.ListAfter(context.Background(), children[0].ID, 0, 1000)
	sawSearch := false
	for _, e := range evs {
		if e.Type == domain.EvtToolCompleted && contains(string(e.PayloadJSON), "file_search") {
			sawSearch = true
		}
	}
	if !sawSearch {
		t.Fatalf("child never executed file_search; events=%d", len(evs))
	}
}
