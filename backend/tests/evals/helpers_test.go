package evals

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/tool/builtin"
	"rivus-agent-backend/tests/testutil"

	"github.com/cloudwego/eino/schema"
)

// ---- 报告（TestMain 必须位于 _test.go） ----

type summary struct {
	Total    int     `json:"total"`
	Passed   int     `json:"passed"`
	PassRate float64 `json:"pass_rate"`
}

// TestMain 在全部测试结束后统一写报告（go test 的 cwd 是本包目录 → 写到 backend/ 根）。
func TestMain(m *testing.M) {
	code := m.Run()
	writeReport()
	os.Exit(code)
}

func writeReport() {
	resultsMu.Lock()
	defer resultsMu.Unlock()

	byCat := map[string]summary{}
	all := summary{}
	for _, r := range results {
		all.Total++
		c := byCat[r.Category]
		c.Total++
		if r.Pass {
			all.Passed++
			c.Passed++
		}
		byCat[r.Category] = c
	}
	if all.Total > 0 {
		all.PassRate = float64(all.Passed) / float64(all.Total)
	}
	for k, c := range byCat {
		if c.Total > 0 {
			c.PassRate = float64(c.Passed) / float64(c.Total)
		}
		byCat[k] = c
	}
	rep := struct {
		Summary    summary            `json:"summary"`
		ByCategory map[string]summary `json:"by_category"`
		Results    []Result           `json:"results"`
	}{Summary: all, ByCategory: byCat, Results: results}

	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		fmt.Println("evals: marshal report:", err)
		return
	}
	if err := os.WriteFile(filepath.Join("..", "..", "eval-report.json"), b, 0o644); err != nil {
		fmt.Println("evals: write report:", err)
	}
}

// ---- 共享 helper ----

// withTempWorkspace 注入临时工作区，测试结束后恢复默认目录。
func withTempWorkspace(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	builtin.SetDirs(ws, "./knowledge")
	t.Cleanup(func() { builtin.SetDirs("./workspace", "./knowledge") })
	return ws
}

// withTempKnowledge 注入临时知识目录。
func withTempKnowledge(t *testing.T) string {
	t.Helper()
	kd := t.TempDir()
	builtin.SetDirs("./workspace", kd)
	t.Cleanup(func() { builtin.SetDirs("./workspace", "./knowledge") })
	return kd
}

// disableHTTPFetch 清空 http_fetch 白名单（禁用态），结束时恢复。
func disableHTTPFetch(t *testing.T) {
	t.Helper()
	builtin.SetHTTPAllowlist(nil)
	t.Cleanup(func() { builtin.SetHTTPAllowlist(nil) })
}

// writeFileRel 在 root 下创建含父目录的相对路径文件（路径用 / 分隔）。
func writeFileRel(root, rel, content string) error {
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0o644)
}

// findEvent 在事件流中找第一个指定类型事件。
func findEvent(t *testing.T, h *testutil.Harness, runID string, typ domain.EventType) (domain.AgentEvent, bool) {
	t.Helper()
	evs, err := h.Events.ListAfter(context.Background(), runID, 0, 500)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	for _, ev := range evs {
		if ev.Type == typ {
			return ev, true
		}
	}
	return domain.AgentEvent{}, false
}

// loopTool 永远返回同一个工具调用（预算/超时场景用）。
func loopTool(id, name, args string) func(int, []*schema.Message) (*schema.Message, error) {
	return func(int, []*schema.Message) (*schema.Message, error) {
		return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
			ID: id, Type: "function",
			Function: schema.FunctionCall{Name: name, Arguments: args},
		}}}, nil
	}
}

// loopToolSlow 同 loopTool，但每次模型调用前睡眠 d（让 max_duration_seconds 预算先于迭代上限触发）。
func loopToolSlow(id, name, args string, d time.Duration) func(int, []*schema.Message) (*schema.Message, error) {
	return func(int, []*schema.Message) (*schema.Message, error) {
		time.Sleep(d)
		return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
			ID: id, Type: "function",
			Function: schema.FunctionCall{Name: name, Arguments: args},
		}}}, nil
	}
}

// reissueToolThenText 审批场景专用：无 Tool 角色消息 → 重发工具调用；有 → 收尾文本。
func reissueToolThenText(id, name, args, finalText string) func(int, []*schema.Message) (*schema.Message, error) {
	return func(_ int, msgs []*schema.Message) (*schema.Message, error) {
		for _, m := range msgs {
			if m.Role == schema.Tool {
				return &schema.Message{Role: schema.Assistant, Content: finalText}, nil
			}
		}
		return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
			ID: id, Type: "function",
			Function: schema.FunctionCall{Name: name, Arguments: args},
		}}}, nil
	}
}

// firstApprovalID 取 run 的第一个审批 ID（GET 返回 {"approvals":[...]} 包装结构）。
func firstApprovalID(t *testing.T, h *testutil.Harness, runID string) string {
	t.Helper()
	code, body := h.Do("GET", "/api/v1/runs/"+runID+"/approvals", nil, nil)
	if code != 200 {
		t.Fatalf("list approvals: %d %s", code, body)
	}
	var wrapped struct {
		Approvals []domain.Approval `json:"approvals"`
	}
	if err := json.Unmarshal(body, &wrapped); err != nil || len(wrapped.Approvals) == 0 {
		t.Fatalf("approvals parse: %v %s", err, body)
	}
	return wrapped.Approvals[0].ID
}
