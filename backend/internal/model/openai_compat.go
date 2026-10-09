package model

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type OpenAICompatConfig struct {
	BaseURL   string
	APIKey    string
	Model     string
	Timeout   time.Duration
	MaxTokens int
}

type openAICompatModel struct {
	cfg   OpenAICompatConfig
	tools []*schema.ToolInfo
}

func NewOpenAICompat(cfg OpenAICompatConfig) (model.ToolCallingChatModel, error) {
	if cfg.BaseURL == "" || cfg.Model == "" {
		return nil, fmt.Errorf("base_url and model are required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 120 * time.Second
	}
	return &openAICompatModel{cfg: cfg}, nil
}

func (m *openAICompatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	cp := *m
	cp.tools = append([]*schema.ToolInfo{}, tools...)
	return &cp, nil
}

func (m *openAICompatModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	req, err := m.buildRequest(input, false)
	if err != nil {
		return nil, err
	}
	resp, err := m.do(ctx, req)
	if err != nil {
		return nil, err
	}
	return toMessage(resp)
}

func (m *openAICompatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	sr, sw := schema.Pipe[*schema.Message](1)
	sw.Send(msg, nil)
	sw.Close()
	return sr, nil
}

var _ = strings.TrimSpace
