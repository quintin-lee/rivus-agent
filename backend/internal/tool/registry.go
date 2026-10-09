package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// RiskLevel 是工具风险分级。
type RiskLevel string

const (
	RiskRead     RiskLevel = "read"
	RiskWrite    RiskLevel = "write"
	RiskExternal RiskLevel = "external_side_effect"
	RiskDenied   RiskLevel = "denied"
)

// ApprovalMode 是审批策略。
type ApprovalMode string

const (
	ApprovalNever       ApprovalMode = "never"
	ApprovalConditional ApprovalMode = "conditional"
	ApprovalAlways      ApprovalMode = "always"
)

// Definition 是工具注册元数据（Eino Tool 只做模型映射，权限/审计/预算走统一入口）。
type Definition struct {
	Name            string
	Description     string
	Version         string
	InputSchema     string // JSON Schema
	RequiredScopes  []string
	Risk            RiskLevel
	Approval        ApprovalMode
	Idempotent      bool
	TimeoutMs       int
	MaxResultBytes  int
	RateLimitPerMin int
	Handler         Handler
}

// Handler 是工具真实实现：ctx 携带 deadline；返回精简结构化结果。
type Handler func(ctx context.Context, args json.RawMessage) (string, error)

// Registry 是原生 Go Tool Registry（后续可选 MCP）。
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Definition
}

func NewRegistry() *Registry { return &Registry{tools: map[string]Definition{}} }

// Register 注册工具；重名直接返回错误，不静默覆盖。
func (r *Registry) Register(d Definition) error {
	if d.Name == "" || d.Handler == nil {
		return fmt.Errorf("tool name and handler are required")
	}
	if d.TimeoutMs <= 0 {
		d.TimeoutMs = 30000
	}
	if d.MaxResultBytes <= 0 {
		d.MaxResultBytes = 32 * 1024
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tools[d.Name]; ok {
		return fmt.Errorf("tool %q already registered", d.Name)
	}
	r.tools[d.Name] = d
	return nil
}

// Get 查询工具。
func (r *Registry) Get(name string) (Definition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.tools[name]
	return d, ok
}

// List 返回全部工具定义。
func (r *Registry) List() []Definition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Definition, 0, len(r.tools))
	for _, d := range r.tools {
		out = append(out, d)
	}
	return out
}
