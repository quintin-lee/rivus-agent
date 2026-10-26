package tool

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
)

// registerTestTool 注册一个只读、永不需要审批的测试工具。
func registerTestTool(t *testing.T, idempotent bool, handler func(ctx context.Context, args json.RawMessage) (string, error)) *Executor {
	t.Helper()
	reg := NewRegistry()
	if err := reg.Register(Definition{
		Name:           "echo",
		Description:    "echo test tool",
		Version:        "1.0.0",
		InputSchema:    `{"type":"object"}`,
		Risk:           RiskRead,
		Approval:       ApprovalNever,
		Idempotent:     idempotent,
		TimeoutMs:      1000,
		MaxResultBytes: 1024,
		Handler:        handler,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	return NewExecutor(reg)
}

// 幂等工具同 key 重复调用必须返回首次真实结果（不是空串），且 handler 只执行一次。
func TestIdempotentCachesRealResult(t *testing.T) {
	var calls atomic.Int32
	ex := registerTestTool(t, true, func(ctx context.Context, args json.RawMessage) (string, error) {
		calls.Add(1)
		return "real-output", nil
	})
	ctx := context.Background()
	req := ExecRequest{RunID: "r1", ToolCallID: "tc1", ToolName: "echo", Args: json.RawMessage(`{}`)}

	first, err := ex.Execute(ctx, req)
	if err != nil {
		t.Fatalf("first execute: %v", err)
	}
	second, err := ex.Execute(ctx, req)
	if err != nil {
		t.Fatalf("second execute: %v", err)
	}
	if first.Output != "real-output" || second.Output != "real-output" {
		t.Fatalf("outputs = %q, %q; want both %q", first.Output, second.Output, "real-output")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("handler called %d times; want 1", got)
	}
}

// 幂等工具失败不入缓存：重试必须真的重新执行。
func TestIdempotentFailureNotCached(t *testing.T) {
	var calls atomic.Int32
	ex := registerTestTool(t, true, func(ctx context.Context, args json.RawMessage) (string, error) {
		if calls.Add(1) == 1 {
			return "", errors.New("boom")
		}
		return "recovered", nil
	})
	ctx := context.Background()
	req := ExecRequest{RunID: "r1", ToolCallID: "tc1", ToolName: "echo", Args: json.RawMessage(`{}`)}

	if _, err := ex.Execute(ctx, req); err == nil {
		t.Fatal("first execute should fail")
	}
	res, err := ex.Execute(ctx, req)
	if err != nil {
		t.Fatalf("retry execute: %v", err)
	}
	if res.Output != "recovered" {
		t.Fatalf("retry output = %q; want %q", res.Output, "recovered")
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("handler called %d times; want 2", got)
	}
}
