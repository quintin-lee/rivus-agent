package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	jsonschema "github.com/eino-contrib/jsonschema"
	"rivus-agent-backend/internal/domain"
	itool "rivus-agent-backend/internal/tool"
)

type RuntimeEvent struct {
	Type     domain.EventType `json:"type"`
	Text     string           `json:"text,omitempty"`
	ToolName string           `json:"tool_name,omitempty"`
	Payload  string           `json:"payload,omitempty"`
	RunID    string           `json:"run_id"`
}

type RunRequest struct {
	RunID         string
	OwnerID       string
	Goal          string
	Mode          string
	Budget        domain.Budget
	ApprovedTools map[string]bool
	CheckpointID  string
}

type ResumeRequest struct {
	RunID         string
	CheckpointID  string
	ApprovedTools map[string]bool
}

type AgentRunner interface {
	Run(ctx context.Context, req RunRequest) (<-chan RuntimeEvent, error)
	Resume(ctx context.Context, req ResumeRequest) (<-chan RuntimeEvent, error)
	Cancel(runID string) error
}

type EinoRunner struct {
	modelFactory   func() (model.ToolCallingChatModel, error)
	registry       *itool.Registry
	executor       *itool.Executor
	checkpoints    adk.CheckPointStore
	instruction    string
	cancels        *cancelRegistry
	onEvent        func(ev RuntimeEvent)
	onNeedApproval func(runID, toolCallID, toolName string, args json.RawMessage)

	mu                sync.RWMutex
	needApprovalByRun map[string]func(runID, toolCallID, toolName string, args json.RawMessage)
}

func NewEinoRunner(
	modelFactory func() (model.ToolCallingChatModel, error),
	registry *itool.Registry,
	executor *itool.Executor,
	checkpoints adk.CheckPointStore,
) *EinoRunner {
	return &EinoRunner{
		modelFactory:      modelFactory,
		registry:          registry,
		executor:          executor,
		checkpoints:       checkpoints,
		instruction:       "你是 Rivus Agent。用中文回答。必须基于工具返回的真实证据行动，禁止编造成功。需要多工具协作时分步调用并观察结果。",
		cancels:           newCancelRegistry(),
		needApprovalByRun: map[string]func(runID, toolCallID, toolName string, args json.RawMessage){},
	}
}

func (r *EinoRunner) OnEvent(fn func(ev RuntimeEvent)) { r.onEvent = fn }

func (r *EinoRunner) OnNeedApproval(fn func(runID, toolCallID, toolName string, args json.RawMessage)) {
	r.onNeedApproval = fn
}

func (r *EinoRunner) SetNeedApprovalHandler(runID string, fn func(runID, toolCallID, toolName string, args json.RawMessage)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if fn == nil {
		delete(r.needApprovalByRun, runID)
		return
	}
	r.needApprovalByRun[runID] = fn
}

func (r *EinoRunner) needApprovalHandler(runID string) func(runID, toolCallID, toolName string, args json.RawMessage) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if fn, ok := r.needApprovalByRun[runID]; ok {
		return fn
	}
	return r.onNeedApproval
}

func (r *EinoRunner) Cancel(runID string) error { return r.cancels.cancel(runID) }

func (r *EinoRunner) DeleteCheckpointsByRun(ctx context.Context, runID string) {
	if s, ok := r.checkpoints.(interface {
		DeleteByRunPrefix(context.Context, string) error
	}); ok {
		_ = s.DeleteByRunPrefix(ctx, runID)
	}
}

func (r *EinoRunner) Run(ctx context.Context, req RunRequest) (<-chan RuntimeEvent, error) {
	return r.executeNew(ctx, req)
}

func (r *EinoRunner) Resume(ctx context.Context, req ResumeRequest) (<-chan RuntimeEvent, error) {
	return r.executeResume(ctx, req)
}

type gatewayTool struct {
	def            itool.Definition
	runID          string
	approved       map[string]bool
	executor       *itool.Executor
	onNeedApproval func(runID, toolCallID, toolName string, args json.RawMessage)
}

func (g *gatewayTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	ti := &schema.ToolInfo{Name: g.def.Name, Desc: g.def.Description}
	var js jsonschema.Schema
	if err := json.Unmarshal([]byte(g.def.InputSchema), &js); err == nil {
		ti.ParamsOneOf = schema.NewParamsOneOfByJSONSchema(&js)
	}
	return ti, nil
}

func (g *gatewayTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	var raw json.RawMessage = json.RawMessage(argumentsInJSON)
	callID := g.def.Name + ":" + shortHash(itool.ArgsHash(raw))
	if g.approved != nil && g.approved[g.def.Name+":"+itool.ArgsHash(raw)] {
		out, err := g.executor.Execute(ctx, itool.ExecRequest{
			RunID: g.runID, ToolName: g.def.Name, Args: raw, Approved: true, ToolCallID: callID,
		})
		if err != nil {
			return "", err
		}
		return out.Output, nil
	}
	out, err := g.executor.Execute(ctx, itool.ExecRequest{
		RunID: g.runID, ToolName: g.def.Name, Args: raw, ToolCallID: callID,
	})
	if err != nil {
		if errors.Is(err, domain.ErrNeedsApproval) && g.onNeedApproval != nil {
			g.onNeedApproval(g.runID, callID, g.def.Name, raw)
		}
		return "", err
	}
	return out.Output, nil
}

func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

func (r *EinoRunner) buildAgent(ctx context.Context, runID string, budget domain.Budget, approved map[string]bool) (*adk.ChatModelAgent, error) {
	base, err := r.modelFactory()
	if err != nil {
		return nil, err
	}
	var tools []tool.BaseTool
	for _, d := range r.registry.List() {
		d := d
		tools = append(tools, &gatewayTool{
			def: d, runID: runID, approved: approved,
			executor: r.executor, onNeedApproval: r.needApprovalHandler(runID),
		})
	}
	withTools, err := base.WithTools(collectInfos(tools))
	if err != nil {
		return nil, err
	}
	maxIter := budget.MaxIterations
	if maxIter <= 0 {
		maxIter = 12
	}
	cfg := &adk.ChatModelAgentConfig{
		Name:          "rivus",
		Description:   "general purpose agent",
		Instruction:   r.instruction,
		Model:         withTools,
		MaxIterations: maxIter,
	}
	cfg.ToolsConfig.Tools = tools
	return adk.NewChatModelAgent(ctx, cfg)
}

func collectInfos(tools []tool.BaseTool) []*schema.ToolInfo {
	var out []*schema.ToolInfo
	for _, t := range tools {
		ti, err := t.Info(context.Background())
		if err == nil {
			out = append(out, ti)
		}
	}
	return out
}

func (r *EinoRunner) executeNew(ctx context.Context, req RunRequest) (<-chan RuntimeEvent, error) {
	agent, err := r.buildAgent(ctx, req.RunID, req.Budget, req.ApprovedTools)
	if err != nil {
		return nil, err
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent: agent, EnableStreaming: true, CheckPointStore: r.checkpoints,
	})
	ctx, cancel := r.cancels.track(req.RunID, ctx)
	out := make(chan RuntimeEvent, 64)
	go func() {
		defer close(out)
		defer cancel()
		r.emit(out, req.RunID, domain.EvtRunStarted, "run started", "", "")
		iter := runner.Run(ctx, []*schema.Message{schema.UserMessage(req.Goal)})
		r.drain(ctx, req.RunID, req.CheckpointID, iter, out)
	}()
	return out, nil
}

func (r *EinoRunner) executeResume(ctx context.Context, req ResumeRequest) (<-chan RuntimeEvent, error) {
	agent, err := r.buildAgent(ctx, req.RunID, domain.Budget{MaxIterations: 12, MaxModelCalls: 30, MaxToolCalls: 50, MaxDurationSeconds: 600}, req.ApprovedTools)
	if err != nil {
		return nil, err
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent: agent, EnableStreaming: true, CheckPointStore: r.checkpoints,
	})
	ctx, cancel := r.cancels.track(req.RunID, ctx)
	out := make(chan RuntimeEvent, 64)
	go func() {
		defer close(out)
		defer cancel()
		r.emit(out, req.RunID, domain.EvtRunResumed, "run resumed", "", "")
		iter, err := runner.Resume(ctx, req.CheckpointID)
		if err != nil {
			r.emit(out, req.RunID, domain.EvtRunFailed, err.Error(), "", "")
			return
		}
		r.drain(ctx, req.RunID, req.CheckpointID, iter, out)
	}()
	return out, nil
}

func (r *EinoRunner) drain(ctx context.Context, runID, checkpointID string, iter *adk.AsyncIterator[*adk.AgentEvent], out chan<- RuntimeEvent) {
	var lastText string
	for {
		select {
		case <-ctx.Done():
			r.emit(out, runID, domain.EvtRunCancelled, ctx.Err().Error(), "", "")
			return
		default:
		}
		ev, ok := iter.Next()
		if !ok {
			break
		}
		if ev == nil {
			continue
		}
		if ev.Err != nil {
			if errors.Is(ev.Err, domain.ErrNeedsApproval) {
				r.emit(out, runID, domain.EvtApprovalRequested, ev.Err.Error(), "", "")
				return
			}
			r.emit(out, runID, domain.EvtModelFailed, ev.Err.Error(), "", "")
			r.emit(out, runID, domain.EvtRunFailed, ev.Err.Error(), "", "")
			return
		}
		if ev.Output != nil && ev.Output.MessageOutput != nil {
			mo := ev.Output.MessageOutput
			text := ""
			if mo.Message != nil {
				text = mo.Message.Content
			}
			if text == "" && mo.MessageStream != nil {
				text = drainStream(mo.MessageStream)
			}
			if text != "" {
				lastText = text
				r.emit(out, runID, domain.EvtModelCompleted, text, "", "")
			}
			if mo.ToolName != "" {
				r.emit(out, runID, domain.EvtToolCompleted, mo.ToolName, mo.ToolName, "")
			}
		}
		if ev.Action != nil && ev.Action.Interrupted != nil {
			r.emit(out, runID, domain.EvtApprovalRequested, "interrupted", "", "")
			return
		}
	}
	_ = checkpointID
	_ = lastText
	r.emit(out, runID, domain.EvtRunFinished, lastText, "", "")
}

func drainStream(sr *schema.StreamReader[*schema.Message]) string {
	defer sr.Close()
	var sb strings.Builder
	for {
		chunk, err := sr.Recv()
		if err != nil {
			break
		}
		if chunk != nil {
			sb.WriteString(chunk.Content)
			if len(sb.String()) > 200000 {
				break
			}
		}
	}
	return sb.String()
}

func (r *EinoRunner) emit(out chan<- RuntimeEvent, runID string, typ domain.EventType, text, tool, payload string) {
	ev := RuntimeEvent{Type: typ, Text: text, ToolName: tool, Payload: payload, RunID: runID}
	select {
	case out <- ev:
	default:
	}
	if r.onEvent != nil {
		r.onEvent(ev)
	}
}

type cancelRegistry struct {
	mu sync.Mutex
	m  map[string]context.CancelFunc
}

func newCancelRegistry() *cancelRegistry { return &cancelRegistry{m: map[string]context.CancelFunc{}} }

func (c *cancelRegistry) track(runID string, ctx context.Context) (context.Context, context.CancelFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if old, ok := c.m[runID]; ok {
		old()
	}
	nctx, cancel := context.WithCancel(ctx)
	c.m[runID] = cancel
	return nctx, func() {
		cancel()
		c.mu.Lock()
		delete(c.m, runID)
		c.mu.Unlock()
	}
}

func (c *cancelRegistry) cancel(runID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if fn, ok := c.m[runID]; ok {
		fn()
		return nil
	}
	return fmt.Errorf("run %s is not running", runID)
}
