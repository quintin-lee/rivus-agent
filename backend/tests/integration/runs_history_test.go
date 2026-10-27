package integration

import (
	"context"
	"encoding/json"
	"testing"

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
