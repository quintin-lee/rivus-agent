package mcp

import (
	"context"
	"strings"
	"sync"
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

// TestClientConcurrent verifies a single Client is safe for concurrent use:
// several goroutines share one Client + FakeTransport and the write→read pair
// is serialized by Client.mu (clean under `go test -race`).
func TestClientConcurrent(t *testing.T) {
	ft := &FakeTransport{Tools: []string{"echo"}, CallResult: "ok"}
	c := NewClient(ft)
	const n = 50
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			out, err := c.Call(context.Background(), "echo", map[string]any{"i": idx})
			if err != nil || out != "ok" {
				t.Errorf("concurrent call i=%d: out=%q err=%v", idx, out, err)
			}
		}(i)
	}
	wg.Wait()
}
