package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
)

// Tool is one entry from an MCP tools/list result.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
}

// Client drives one MCP server over a Transport (initialize → tools/list → tools/call).
type Client struct {
	transport Transport
	id        int32
}

func NewClient(t Transport) *Client { return &Client{transport: t} }

func (c *Client) nextID() int { return int(atomic.AddInt32(&c.id, 1)) }

// rpc sends one JSON-RPC request and returns its response body (the "result" field).
func (c *Client) rpc(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID()
	req, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	if err := c.transport.WriteLine(req); err != nil {
		return nil, fmt.Errorf("mcp write %s: %w", method, err)
	}
	line, err := c.transport.ReadLine()
	if err != nil {
		return nil, fmt.Errorf("mcp server disconnected reading %s: %w", method, err)
	}
	var resp struct {
		ID  int `json:"id"`
		Err *struct {
			Message string `json:"message"`
		} `json:"error"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("mcp unmarshal %s: %w", method, err)
	}
	if resp.Err != nil {
		return nil, fmt.Errorf("mcp %s: %s", method, resp.Err.Message)
	}
	return resp.Result, nil
}

func (c *Client) Initialize(ctx context.Context) error {
	_, err := c.rpc(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "rivus"},
	})
	return err
}

func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	res, err := c.rpc(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var out struct {
		Tools []Tool `json:"tools"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return nil, err
	}
	return out.Tools, nil
}

// Call invokes tools/call and maps the content blocks to a joined string.
// A result with isError=true is returned as a Go error.
func (c *Client) Call(ctx context.Context, name string, arguments map[string]any) (string, error) {
	res, err := c.rpc(ctx, "tools/call", map[string]any{"name": name, "arguments": arguments})
	if err != nil {
		return "", err
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return "", err
	}
	var sb []string
	for _, b := range out.Content {
		if b.Type == "text" {
			sb = append(sb, b.Text)
		}
	}
	if out.IsError {
		return "", fmt.Errorf("mcp tool %s error: %s", name, joinText(sb))
	}
	return joinText(sb), nil
}

func joinText(p []string) string {
	s := ""
	for i, v := range p {
		if i > 0 {
			s += "\n"
		}
		s += v
	}
	return s
}
