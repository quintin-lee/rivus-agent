package integration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/tests/testutil"
)

// GET /api/v1/runs 不带 session_id = 跨会话运行历史（仅顶层 run，子 agent run 排除）。
func TestListRunsAcrossSessions(t *testing.T) {
	h := testutil.New(t)
	ses1 := h.CreateSession("one")
	ses2 := h.CreateSession("two")

	id1, code, _ := h.CreateRun(ses1, fixturePayload(t, ses1), "hist-1")
	if code != 201 {
		t.Fatalf("create run1: %d", code)
	}
	id2, code, _ := h.CreateRun(ses2, fixturePayload(t, ses2), "hist-2")
	if code != 201 {
		t.Fatalf("create run2: %d", code)
	}

	// 直接落一条子 run（模拟 tasks 工具产生的子 agent run）
	if err := h.Tasks.CreateRun(context.Background(), &domain.Run{
		ID: "child_x", SessionID: ses1, OwnerID: "default",
		Status: domain.RunSucceeded, Mode: "react", Goal: "child goal",
		ParentRunID: id1, Attempt: 1,
	}); err != nil {
		t.Fatal(err)
	}

	code, body := h.Do("GET", "/api/v1/runs", nil, nil)
	if code != 200 {
		t.Fatalf("GET /runs: %d %s", code, body)
	}
	var out struct {
		Runs []domain.Run `json:"runs"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, r := range out.Runs {
		ids[r.ID] = true
	}
	if !ids[id1] || !ids[id2] {
		t.Fatalf("expected both session runs in history, got %v", ids)
	}
	if ids["child_x"] {
		t.Fatal("child run must be excluded from history")
	}

	// 带 session_id 的原行为不受影响（会话列表不过滤子 run —— 既有行为，保持不变）
	code, body = h.Do("GET", "/api/v1/runs?session_id="+ses1, nil, nil)
	if code != 200 {
		t.Fatalf("GET /runs?session_id: %d %s", code, body)
	}
	out.Runs = nil
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, r := range out.Runs {
		got[r.ID] = true
	}
	if !got[id1] || !got["child_x"] {
		t.Fatalf("session-scoped list unchanged behavior: want %s and child_x, got %v", id1, got)
	}
}

func TestListRunsStatusFilter(t *testing.T) {
	h := testutil.New(t)
	ses := h.CreateSession("sf")
	id1, _, _ := h.CreateRun(ses, map[string]any{"goal": "ok-run"}, "sf-ok")
	// 让 run1 成功
	h.Model.EnqueueText("done")
	h.WaitTerminal(id1, 30*time.Second)
	// run2 走预算上限失败
	payload := map[string]any{"goal": "bad-run", "budget": map[string]any{"max_duration_seconds": 120,
		"max_model_calls": 10, "max_tool_calls": 10, "max_iterations": 6, "max_output_bytes": 10}}
	h.Model.EnqueueText("this output is far longer than ten bytes for sure")
	id2, _, _ := h.CreateRun(ses, payload, "sf-bad")
	run2 := h.WaitTerminal(id2, 30*time.Second)
	if run2.Status != domain.RunFailed {
		t.Fatalf("run2 = %s, want failed", run2.Status)
	}

	// 非法 status → 400
	code, _ := h.Do("GET", "/api/v1/runs?status=bogus", nil, nil)
	if code != 400 {
		t.Fatalf("invalid status code = %d, want 400", code)
	}
	// 过滤 failed 只回败者
	code, body := h.Do("GET", "/api/v1/runs?status=failed", nil, nil)
	if code != 200 {
		t.Fatalf("filtered code = %d", code)
	}
	var out struct {
		Runs []domain.Run `json:"runs"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Runs) != 1 || out.Runs[0].ID != id2 {
		t.Fatalf("failed filter = %v, want [%s]", idsOf(out.Runs), id2)
	}
	// 无过滤两者都有
	code, body = h.Do("GET", "/api/v1/runs", nil, nil)
	if code != 200 {
		t.Fatalf("unfiltered code = %d", code)
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, r := range out.Runs {
		found[r.ID] = true
	}
	if !found[id1] || !found[id2] {
		t.Fatalf("unfiltered missing runs: %v vs {%s,%s}", found, id1, id2)
	}
}

func idsOf(runs []domain.Run) []string {
	out := make([]string, 0, len(runs))
	for _, r := range runs {
		out = append(out, r.ID)
	}
	return out
}
