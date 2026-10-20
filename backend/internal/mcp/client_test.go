package mcp

import (
	"context"
	"strings"
	"testing"
)

func TestClientListAndCall(t *testing.T) {
	ft := &FakeTransport{Tools: []string{"echo", "sum"}, CallResult: "42"}
	c := NewClient(ft)
	if err := c.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	tools, err := c.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v", tools)
	}
	out, err := c.Call(context.Background(), "echo", map[string]any{"x": 1})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if out != "42" {
		t.Fatalf("call output = %q, want 42", out)
	}
	// isError result maps to a Go error
	ft.CallError = true
	if _, err := c.Call(context.Background(), "echo", nil); err == nil || !strings.Contains(err.Error(), "mcp") {
		t.Fatalf("isError must map to error, got %v", err)
	}
}
