package model

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
)

var httpClient = &http.Client{
	Timeout: 180 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        50,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	},
}

type chatRequest struct {
	Model     string     `json:"model"`
	Messages  []chatMsg  `json:"messages"`
	Tools     []chatTool `json:"tools,omitempty"`
	MaxTokens int        `json:"max_tokens,omitempty"`
	Stream    bool       `json:"stream"`
}

type chatMsg struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type chatTool struct {
	Type     string     `json:"type"`
	Function chatToolFn `json:"function"`
}

type chatToolFn struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

type chatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type chatResponse struct {
	ID      string `json:"id"`
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Role      string         `json:"role"`
			Content   string         `json:"content"`
			ToolCalls []chatToolCall `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage map[string]any `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

func (m *openAICompatModel) buildRequest(input []*schema.Message, stream bool) (*chatRequest, error) {
	msgs := make([]chatMsg, 0, len(input))
	for _, in := range input {
		cm := chatMsg{Role: string(in.Role), Content: in.Content}
		for _, tc := range in.ToolCalls {
			call := chatToolCall{ID: tc.ID, Type: "function"}
			call.Function.Name = tc.Function.Name
			call.Function.Arguments = tc.Function.Arguments
			cm.ToolCalls = append(cm.ToolCalls, call)
		}
		if in.ToolCallID != "" {
			cm.ToolCallID = in.ToolCallID
		}
		if cm.Role == "" {
			cm.Role = "user"
		}
		msgs = append(msgs, cm)
	}
	req := &chatRequest{Model: m.cfg.Model, Messages: msgs, Stream: stream, MaxTokens: m.cfg.MaxTokens}
	for _, t := range m.tools {
		var params any = map[string]any{"type": "object"}
		if t.ParamsOneOf != nil {
			if b, err := json.Marshal(t.ParamsOneOf); err == nil {
				var decoded any
				if err := json.Unmarshal(b, &decoded); err == nil && decoded != nil {
					params = decoded
				}
			}
		}
		req.Tools = append(req.Tools, chatTool{Type: "function", Function: chatToolFn{
			Name: t.Name, Description: t.Desc, Parameters: params,
		}})
	}
	return req, nil
}

func (m *openAICompatModel) do(ctx context.Context, req *chatRequest) (*chatResponse, error) {
	body, _ := json.Marshal(req)
	url := strings.TrimRight(m.cfg.BaseURL, "/") + "/chat/completions"
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	if m.cfg.APIKey != "" {
		hreq.Header.Set("Authorization", "Bearer "+m.cfg.APIKey)
	}
	timeout := m.cfg.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	hreq = hreq.WithContext(tctx)
	resp, err := httpClient.Do(hreq)
	if err != nil {
		return nil, Classify(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, Classify(err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		retryable := resp.StatusCode == 429 || resp.StatusCode >= 500
		return nil, &RetryError{Status: resp.StatusCode, Body: truncate(string(b), 2000), Retryable: retryable}
	}
	var out chatResponse
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("decode model response: %w", err)
	}
	if out.Error != nil {
		return nil, fmt.Errorf("model error: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("empty model response")
	}
	return &out, nil
}

func toMessage(resp *chatResponse) (*schema.Message, error) {
	c := resp.Choices[0].Message
	msg := &schema.Message{Role: schema.Assistant, Content: c.Content}
	for _, tc := range c.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, schema.ToolCall{
			ID:   tc.ID,
			Type: "function",
			Function: schema.FunctionCall{
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			},
		})
	}
	return msg, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
