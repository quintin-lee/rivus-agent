package mcp

import (
	"context"
	"encoding/json"
	"io"
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

// notifyScriptTransport 按预排队列吐行（通知行 + 响应行），用于验证 rpc 跳过通知。
type notifyScriptTransport struct {
	mu    sync.Mutex
	lines [][]byte
	sent  []jsonRequest
}

var _ Transport = (*notifyScriptTransport)(nil)

func (t *notifyScriptTransport) WriteLine(p []byte) error {
	var req jsonRequest
	if err := json.Unmarshal(p, &req); err != nil {
		return err
	}
	t.mu.Lock()
	t.sent = append(t.sent, req)
	t.mu.Unlock()
	return nil
}

func (t *notifyScriptTransport) ReadLine() ([]byte, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.lines) == 0 {
		return nil, io.EOF
	}
	line := t.lines[0]
	t.lines = t.lines[1:]
	return line, nil
}

func (t *notifyScriptTransport) Stop() error { return nil }

func TestRPCSkipsNotificationsAndDispatches(t *testing.T) {
	tr := &notifyScriptTransport{lines: [][]byte{
		[]byte(`{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}`),
		[]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"read"}]}}`),
	}}
	c := NewClient(tr)
	var gotMethod string
	var gotParams json.RawMessage
	c.SetNotificationHandler(func(m string, p json.RawMessage) {
		gotMethod, gotParams = m, p
	})
	tools, err := c.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "read" {
		t.Fatalf("tools = %+v", tools)
	}
	if gotMethod != "notifications/tools/list_changed" {
		t.Fatalf("notify method = %q", gotMethod)
	}
	if len(gotParams) != 0 {
		t.Fatalf("params = %s", gotParams)
	}

	// 无 handler 通知不 panic
	tr2 := &notifyScriptTransport{lines: [][]byte{
		[]byte(`{"jsonrpc":"2.0","method":"notifications/other"}`),
		[]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`),
	}}
	c2 := NewClient(tr2)
	c2.SetNotificationHandler(nil)
	if _, err := c2.ListTools(context.Background()); err != nil {
		t.Fatal(err)
	}
}
