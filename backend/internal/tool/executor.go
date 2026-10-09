package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"golang.org/x/time/rate"
	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/observability"
	"rivus-agent-backend/internal/security"
)

type ExecRequest struct {
	RunID      string
	StepID     string
	ToolCallID string
	ToolName   string
	Args       json.RawMessage
	Scopes     []string
	Approved   bool
}

type ExecResult struct {
	Output     string
	OutputSize int
	DurationMs int64
	Retryable  bool
}

type Executor struct {
	registry *Registry
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	seen     map[string]string
}

func NewExecutor(r *Registry) *Executor {
	return &Executor{registry: r, limiters: map[string]*rate.Limiter{}, seen: map[string]string{}}
}

func (e *Executor) Execute(ctx context.Context, req ExecRequest) (*ExecResult, error) {
	def, ok := e.registry.Get(req.ToolName)
	if !ok {
		return nil, domain.Wrap("tool_not_found", "unknown tool "+req.ToolName, false, nil)
	}
	dec := CheckPolicy(def, req.Scopes, req.Approved)
	if dec.NeedApproval {
		return nil, domain.ErrNeedsApproval
	}
	if !dec.Allow {
		return nil, domain.Wrap("tool_denied", dec.DenyReason, false, nil)
	}
	if def.Idempotent {
		key := req.RunID + ":" + req.ToolCallID
		e.mu.Lock()
		if prev, dup := e.seen[key]; dup {
			e.mu.Unlock()
			return &ExecResult{Output: prev, OutputSize: len(prev)}, nil
		}
		e.mu.Unlock()
		defer func() {
			e.mu.Lock()
			e.seen[key] = ""
			e.mu.Unlock()
		}()
	}
	if !e.allow(req.ToolName, def.RateLimitPerMin) {
		return nil, domain.Wrap("rate_limited", "tool rate limit exceeded", true, nil)
	}
	timeout := time.Duration(def.TimeoutMs) * time.Millisecond
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	out, err := def.Handler(tctx, req.Args)
	lat := time.Since(start).Milliseconds()
	if err != nil {
		observability.ObserveTool(req.ToolName, false, lat)
		retryable := tctx.Err() == nil
		return nil, domain.Wrap("tool_failed", security.Redact(err.Error()), retryable, err)
	}
	if len(out) > def.MaxResultBytes {
		out = out[:def.MaxResultBytes] + "...[truncated]"
	}
	observability.ObserveTool(req.ToolName, true, lat)
	if def.Idempotent {
		key := req.RunID + ":" + req.ToolCallID
		e.mu.Lock()
		e.seen[key] = out
		e.mu.Unlock()
	}
	return &ExecResult{Output: out, OutputSize: len(out), DurationMs: lat}, nil
}

func (e *Executor) allow(name string, perMin int) bool {
	if perMin <= 0 {
		return true
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	l, ok := e.limiters[name]
	if !ok {
		l = rate.NewLimiter(rate.Every(time.Minute/time.Duration(perMin)), perMin)
		e.limiters[name] = l
	}
	return l.Allow()
}

var _ = fmt.Sprint
