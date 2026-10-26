package integration

import (
	"strings"
	"testing"
	"time"

	"rivus-agent-backend/tests/testutil"
)

// run1 写记忆 → API 可查 → run2 memory_read 读回内容（跨 run 持久 + 指令注入链路）。
func TestMemoryAcrossRuns(t *testing.T) {
	h := testutil.New(t)
	ses := h.CreateSession("mem")

	// run1：写记忆
	h.Model.EnqueueToolCall("tc1", "memory_write", `{"content":"用户偏好简洁回复"}`)
	h.Model.EnqueueText("已记住")
	id1, code, _ := h.CreateRun(ses, fixturePayload(t, ses), "mem-1")
	if code != 201 {
		t.Fatalf("create run1: %d", code)
	}
	run1 := h.WaitTerminal(id1, 30*time.Second)
	if run1.Status != "succeeded" {
		t.Fatalf("run1 status=%s summary=%s", run1.Status, run1.ErrorSummary)
	}

	// API：会话记忆可查
	c, body := h.Do("GET", "/api/v1/sessions/"+ses+"/memories", nil, nil)
	if c != 200 || !strings.Contains(string(body), "用户偏好简洁回复") {
		t.Fatalf("GET memories: code=%d body=%s", c, body)
	}

	// run2：读记忆并复述
	h.Model.EnqueueToolCall("tc2", "memory_read", `{}`)
	h.Model.EnqueueText("记忆内容：用户偏好简洁回复")
	id2, code, _ := h.CreateRun(ses, fixturePayload(t, ses), "mem-2")
	if code != 201 {
		t.Fatalf("create run2: %d", code)
	}
	run2 := h.WaitTerminal(id2, 30*time.Second)
	if run2.Status != "succeeded" {
		t.Fatalf("run2 status=%s summary=%s", run2.Status, run2.ErrorSummary)
	}
	if !strings.Contains(run2.ResultJSON, "用户偏好简洁回复") {
		t.Fatalf("run2 result missing memory content: %q", run2.ResultJSON)
	}
}
