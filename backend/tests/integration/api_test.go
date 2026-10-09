package integration

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/tool"
	"rivus-agent-backend/tests/testutil"
)

func TestSessionCRUD(t *testing.T) {
	h := testutil.New(t)
	ses := h.CreateSession("demo")
	code, body := h.Do("GET", "/api/v1/sessions/"+ses, nil, nil)
	if code != 200 {
		t.Fatalf("get session: %d %s", code, body)
	}
	code, _ = h.Do("GET", "/api/v1/sessions/ses_nope", nil, nil)
	if code != 404 {
		t.Fatalf("missing session must 404, got %d", code)
	}
}

func TestCreateRunValidation(t *testing.T) {
	h := testutil.New(t)
	ses := h.CreateSession("s")
	id, code, _ := h.CreateRun(ses, map[string]any{"goal": ""}, "")
	if code != 400 || id != "" {
		t.Fatalf("empty goal must 400, got %d id=%q", code, id)
	}
	_, code, _ = h.CreateRun("ses_nope", map[string]any{"goal": "hi"}, "")
	if code != 400 {
		t.Fatalf("unknown session must 400, got %d", code)
	}
}

func TestCreateRunIdempotent(t *testing.T) {
	h := testutil.New(t)
	ses := h.CreateSession("s")
	payload := map[string]any{"goal": "hi"}
	id1, code1, _ := h.CreateRun(ses, payload, "key-1")
	id2, code2, out := h.CreateRun(ses, map[string]any{"goal": "hi"}, "key-1")
	if code1 != 201 || code2 != 201 || id1 != id2 || out["duplicated"] != true {
		t.Fatalf("idempotency broken: %d/%d %q vs %q dup=%v", code1, code2, id1, id2, out["duplicated"])
	}
}

func fixturePayload(t *testing.T, ses string) map[string]any {
	t.Helper()
	b, err := os.ReadFile("../fixtures/run_request.json")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	m["session_id"] = ses
	return m
}

func TestRunSuccessE2E(t *testing.T) {
	h := testutil.New(t)
	h.Model.EnqueueToolCall("tc1", "knowledge_search", `{"query":"重构","top_k":3}`)
	h.Model.EnqueueText("输出风险列表：N+1 查询（证据：knowledge_search 结果），每条建议有文件或代码证据。")
	ses := h.CreateSession("s")
	id, code, _ := h.CreateRun(ses, fixturePayload(t, ses), "e2e-1")
	if code != 201 {
		t.Fatalf("create run: %d", code)
	}
	run := h.WaitTerminal(id, 30*time.Second)
	if run.Status != domain.RunSucceeded {
		t.Fatalf("want succeeded, got %s (%s)", run.Status, run.ErrorSummary)
	}
	if !strings.Contains(run.ResultJSON, `"passed":true`) {
		t.Fatalf("verifier report missing: %s", run.ResultJSON)
	}
}

func TestSSE(t *testing.T) {
	h := testutil.New(t)
	h.Model.EnqueueText("done")
	ses := h.CreateSession("s")
	id, _, _ := h.CreateRun(ses, map[string]any{"goal": "hi"}, "sse-1")
	h.WaitTerminal(id, 30*time.Second)
	code, body := h.Do("GET", "/api/v1/runs/"+id+"/events", nil, map[string]string{"Accept": "text/event-stream"})
	if code != 200 {
		t.Fatalf("sse status %d", code)
	}
	text := string(body)
	for _, want := range []string{"id: ", "event: run.created", "data: "} {
		if !strings.Contains(text, want) {
			t.Fatalf("sse missing %q in:\n%s", want, text)
		}
	}
	code, body2 := h.Do("GET", "/api/v1/runs/"+id+"/events?after=999999", nil, nil)
	if code != 200 || strings.Contains(string(body2), "event: ") {
		t.Fatalf("after filter broken: %d %q", code, body2)
	}
}

func blockToolDef() tool.Definition {
	return tool.Definition{
		Name: "block_tool", Description: "blocks until cancelled (test only)", Version: "v1",
		InputSchema:    `{"type":"object","properties":{}}`,
		Risk:           tool.RiskRead,
		Approval:       tool.ApprovalNever,
		TimeoutMs:      30000,
		MaxResultBytes: 1024,
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		},
	}
}

func TestCancel(t *testing.T) {
	h := testutil.New(t, blockToolDef())
	h.Model.EnqueueToolCall("tc1", "block_tool", `{}`)
	ses := h.CreateSession("s")
	id, _, _ := h.CreateRun(ses, map[string]any{"goal": "slow task"}, "cancel-1")
	time.Sleep(500 * time.Millisecond)
	code, _ := h.Do("POST", "/api/v1/runs/"+id+"/cancel", nil, nil)
	if code != 200 {
		t.Fatalf("cancel: %d", code)
	}
	run := h.WaitTerminal(id, 30*time.Second)
	if run.Status != domain.RunCancelled {
		t.Fatalf("want cancelled, got %s", run.Status)
	}
}
