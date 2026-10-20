package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// FakeTransport is an in-memory Transport for tests. It captures each request
// (method + id) on WriteLine and returns the canned response for that method on
// the next ReadLine. The client drives one request at a time, so a single
// "last request" slot is enough for correlation.
type FakeTransport struct {
	Tools      []string // names returned by tools/list
	CallResult string   // text returned for a successful tools/call
	CallError  bool     // if true, tools/call returns isError
	Crash      bool     // if true, the server is disconnected

	mu      sync.Mutex
	pending *jsonRequest
}

type jsonRequest struct {
	ID     int             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// Compile-time check that FakeTransport satisfies Transport.
var _ Transport = (*FakeTransport)(nil)

// FakeTransportFactory builds FakeTransports for a named server spec.
type FakeTransportFactory struct {
	ByServer map[string]*FakeTransport
}

func NewFakeTransportFactory() *FakeTransportFactory {
	return &FakeTransportFactory{ByServer: map[string]*FakeTransport{}}
}

func (f *FakeTransportFactory) New(_ context.Context, spec ServerSpec) (Transport, error) {
	if ft, ok := f.ByServer[spec.Name]; ok {
		return ft, nil
	}
	ft := &FakeTransport{Tools: []string{"echo"}}
	f.ByServer[spec.Name] = ft
	return ft, nil
}

// WriteLine decodes the request method + id and holds it for the next ReadLine.
func (ft *FakeTransport) WriteLine(p []byte) error {
	if ft.Crash {
		return fmt.Errorf("mcp fake: crashed")
	}
	var req jsonRequest
	if err := json.Unmarshal(p, &req); err != nil {
		return fmt.Errorf("mcp fake: bad request: %w", err)
	}
	ft.mu.Lock()
	ft.pending = &req
	ft.mu.Unlock()
	return nil
}

// ReadLine emits the canned JSON-RPC response for the last request method.
func (ft *FakeTransport) ReadLine() ([]byte, error) {
	if ft.Crash {
		return nil, fmt.Errorf("mcp fake: disconnected")
	}
	ft.mu.Lock()
	req := ft.pending
	ft.pending = nil
	ft.mu.Unlock()
	if req == nil {
		return nil, fmt.Errorf("mcp fake: ReadLine before WriteLine")
	}
	body, err := ft.cannedFor(req)
	if err != nil {
		return nil, err
	}
	return []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":%s}`, req.ID, body)), nil
}

// cannedFor builds the "result" JSON body for a request.
func (ft *FakeTransport) cannedFor(req *jsonRequest) (string, error) {
	switch req.Method {
	case "initialize":
		return `{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"fake"}}`, nil
	case "tools/list":
		type mcpTool struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		tools := make([]mcpTool, 0, len(ft.Tools))
		for _, n := range ft.Tools {
			tools = append(tools, mcpTool{Name: n, Description: "fake tool"})
		}
		b, err := json.Marshal(map[string]any{"tools": tools})
		return string(b), err
	case "tools/call":
		if ft.CallError {
			return `{"content":[{"type":"text","text":"boom"}],"isError":true}`, nil
		}
		b, _ := json.Marshal(map[string]any{
			"content": []any{map[string]string{"type": "text", "text": ft.CallResult}},
		})
		return string(b), nil
	default:
		return "", fmt.Errorf("mcp fake: unknown method %q", req.Method)
	}
}

func (ft *FakeTransport) Stop() error { return nil }
