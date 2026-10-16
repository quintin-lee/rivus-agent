package integration

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/tests/testutil"
)

// TestApprovalListEndpoint 覆盖 GET /runs/{id}/approvals 的 200/空/404/跨租户。
func TestApprovalListEndpoint(t *testing.T) {
	h := testutil.New(t, publishToolDef())

	// 造一个触发 waiting_approval 的 run（SetFallback body 与 approval_test.go 一致：
	// 带 tool call 触发审批，收到 Tool 结果后给 final 证据文本）。
	finalText := "输出风险列表：已发布（证据：publish_draft 结果），每条建议有文件或代码证据。"
	h.Model.SetFallback(func(_ int, input []*schema.Message) (*schema.Message, error) {
		for _, m := range input {
			if m.Role == schema.Tool {
				return &schema.Message{Role: schema.Assistant, Content: finalText}, nil
			}
		}
		return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
			ID: "tc1", Type: "function",
			Function: schema.FunctionCall{Name: "publish_draft", Arguments: `{"title":"hello"}`},
		}}}, nil
	})
	ses := h.CreateSession("s")
	id, code, _ := h.CreateRun(ses, fixturePayload(t, ses), "list-1")
	if code != 201 {
		t.Fatalf("create run: %d", code)
	}
	a := waitApproval(t, h, id) // 造出 1 条 pending 记录

	// 用例 1：审批后的 run → 200，approvals 含 1 条，字段与 waitApproval 记录一致
	_, body := h.Do("GET", "/api/v1/runs/"+id+"/approvals", nil, nil)
	got := parseApprovals(t, body)
	if len(got) != 1 || got[0].ID != a.ID {
		t.Fatalf("want 1 approval %s, got %+v", a.ID, got)
	}
	if got[0].ToolName != a.ToolName || got[0].Status != a.Status {
		t.Fatalf("approval fields mismatch: %+v", got[0])
	}

	// 用例 2：无审批的 run → 200 {"approvals":[]}（非 null）。
	// 换掉 fallback：不触发 tool call，直接给 final 证据 → run 终态、无 pending 记录。
	h.Model.SetFallback(func(_ int, _ []*schema.Message) (*schema.Message, error) {
		return &schema.Message{Role: schema.Assistant, Content: "done（证据：x）"}, nil
	})
	ses2 := h.CreateSession("s2")
	id2, code2, _ := h.CreateRun(ses2, fixturePayload(t, ses2), "list-2")
	if code2 != 201 {
		t.Fatalf("create run2: %d", code2)
	}
	_ = h.WaitTerminal(id2, 30*time.Second)
	_, body2 := h.Do("GET", "/api/v1/runs/"+id2+"/approvals", nil, nil)
	if !contains(string(body2), "\"approvals\":[]") {
		t.Fatalf("want empty approvals [], got %s", body2)
	}

	// 用例 3：不存在的 run id → 404
	code3, _ := h.Do("GET", "/api/v1/runs/does-not-exist/approvals", nil, nil)
	if code3 != 404 {
		t.Fatalf("want 404 for unknown run, got %d", code3)
	}

	// 用例 4：非 owner 读他人 run → 404（跨租户，delete_session_test.go:21 先例）
	code4, _ := h.Do("GET", "/api/v1/runs/"+id+"/approvals", nil, map[string]string{"X-Owner-ID": "intruder"})
	if code4 != 404 {
		t.Fatalf("want 404 for cross-tenant, got %d", code4)
	}
}

func parseApprovals(t *testing.T, body []byte) []domain.Approval {
	t.Helper()
	var out struct {
		Approvals []domain.Approval `json:"approvals"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("parse approvals: %v", err)
	}
	return out.Approvals
}
