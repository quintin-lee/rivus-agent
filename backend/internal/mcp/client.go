package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// Tool is one entry from an MCP tools/list result.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
}

// Client drives one MCP server over a Transport (initialize → tools/list →
// tools/call). A Client is goroutine-safe: the write→read RPC pair is
// serialized by mu, so concurrent calls on one client (e.g. several subagents
// sharing a server) never interleave request/response lines.
type Client struct {
	transport Transport
	id        int32
	onNotify  func(method string, params json.RawMessage)

	mu sync.Mutex // serializes the one-in-flight write→read pair
}

func NewClient(t Transport) *Client { return &Client{transport: t} }

// SetNotificationHandler 注册服务器通知回调（可为 nil）。回调在 rpc 释放锁之后调用，
// 因此回调内不得重入同一 Client 的 rpc（会与自身死锁）。
func (c *Client) SetNotificationHandler(fn func(method string, params json.RawMessage)) {
	c.onNotify = fn
}

func (c *Client) nextID() int { return int(atomic.AddInt32(&c.id, 1)) }

// mcpNotification 是一次收到的服务器通知（有 method 无 id 的 JSON-RPC 行）。
type mcpNotification struct {
	method string
	params json.RawMessage
}

// rpc 发送一个请求并返回响应 result；途中收到的通知行先缓存，释放锁后统一分发。
func (c *Client) rpc(ctx context.Context, method string, params any) (json.RawMessage, error) {
	result, notes, err := c.rpcLocked(ctx, method, params)
	for _, n := range notes {
		if c.onNotify != nil {
			c.onNotify(n.method, n.params)
		}
	}
	return result, err
}

func (c *Client) rpcLocked(ctx context.Context, method string, params any) (json.RawMessage, []mcpNotification, error) {
	id := c.nextID()
	req, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return nil, nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.transport.WriteLine(req); err != nil {
		return nil, nil, fmt.Errorf("mcp write %s: %w", method, err)
	}
	var notes []mcpNotification
	// 循环读取：通知行（有 method 无 id；本客户端请求 id 从 1 递增）跳过并缓存，
	// 直到读到与请求 id 匹配的响应为止。
	for {
		// ReadLine may return the last response line together with io.EOF when
		// the server closes the stream; that data is still a valid response, so
		// only error when there is no data at all.
		line, readErr := c.transport.ReadLine()
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, nil, fmt.Errorf("mcp server disconnected reading %s: %w", method, readErr)
		}
		var resp struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Err    *struct {
				Message string `json:"message"`
			} `json:"error"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(line, &resp); err != nil {
			return nil, nil, fmt.Errorf("mcp unmarshal %s: %w", method, err)
		}
		if resp.Method != "" && resp.ID == 0 {
			notes = append(notes, mcpNotification{method: resp.Method, params: resp.Params})
			continue
		}
		if resp.Err != nil {
			return nil, nil, fmt.Errorf("mcp %s: %s", method, resp.Err.Message)
		}
		if resp.ID != id {
			return nil, nil, fmt.Errorf("mcp %s: response id %d does not match request id %d", method, resp.ID, id)
		}
		return resp.Result, notes, nil
	}
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
	params := map[string]any{"name": name}
	if arguments != nil {
		params["arguments"] = arguments
	}
	res, err := c.rpc(ctx, "tools/call", params)
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
