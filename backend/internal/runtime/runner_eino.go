package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"rivus-agent-backend/internal/domain"
	itool "rivus-agent-backend/internal/tool"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	jsonschema "github.com/eino-contrib/jsonschema"
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
	SessionID     string // 顶层 run 的会话（子 agent 不经 RunRequest）
	MemoryContext string // 预格式化的记忆块（""=无）；由 runtime.run 组装
}

type ResumeRequest struct {
	RunID         string
	CheckpointID  string
	ApprovedTools map[string]bool
	Budget        domain.Budget
}

// resolveResumeBudget：Resume 未携带预算（全零）时退回服务端默认上限；
// 携带则原样使用（RunService 创建时已 ClampByServer，只收紧不放宽）。
func resolveResumeBudget(b domain.Budget) domain.Budget {
	if b == (domain.Budget{}) {
		return domain.Budget{MaxIterations: 12, MaxModelCalls: 30, MaxToolCalls: 50, MaxDurationSeconds: 600}
	}
	return b
}

type AgentRunner interface {
	Run(ctx context.Context, req RunRequest) (<-chan RuntimeEvent, error)
	Resume(ctx context.Context, req ResumeRequest) (<-chan RuntimeEvent, error)
	Cancel(runID string) error
}

type EinoRunner struct {
	modelFactory    func() (model.ToolCallingChatModel, error)
	registry        *itool.Registry
	executor        *itool.Executor
	checkpoints     adk.CheckPointStore
	instruction     string
	baseInstruction string
	cancels         *cancelRegistry
	onEvent         func(ev RuntimeEvent)
	onNeedApproval  func(runID, toolCallID, toolName string, args json.RawMessage)

	mu                sync.RWMutex
	needApprovalByRun map[string]func(runID, toolCallID, toolName string, args json.RawMessage)

	toolObsMu sync.Mutex
	toolObs   map[string]toolObservation
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
		baseInstruction:   "你是 Rivus Agent。用中文回答。必须基于工具返回的真实证据行动，禁止编造成功。需要多工具协作时分步调用并观察结果。",
		cancels:           newCancelRegistry(),
		needApprovalByRun: map[string]func(runID, toolCallID, toolName string, args json.RawMessage){},
	}
}

// SetSkillInstructions rebuilds the instruction as base + optional skills
// section, so repeated enables don't accumulate and an empty listing removes
// the section. Idempotent: safe to call on every enable-save.
func (r *EinoRunner) SetSkillInstructions(listing string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if listing == "" {
		r.instruction = r.baseInstruction
		return
	}
	r.instruction = r.baseInstruction + "\n\n可用技能：\n" + listing
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

// toolObservation 记录一次成功工具调用的 args/output 摘要（各截 1KB）。
type toolObservation struct {
	args   string
	output string
}

// recordToolObserved 由 gatewayTool 在 Execute 成功后调用；按 runID|toolName 隔离
// （并行子 agent 同名工具互不串扰）。map 超过 256 条整体清空，防长期运行泄漏。
func (r *EinoRunner) recordToolObserved(runID, toolName string, args json.RawMessage, output string) {
	const maxObs = 1024
	a := string(args)
	if len(a) > maxObs {
		a = a[:maxObs]
	}
	o := output
	if len(o) > maxObs {
		o = o[:maxObs]
	}
	r.toolObsMu.Lock()
	if r.toolObs == nil {
		r.toolObs = map[string]toolObservation{}
	}
	if len(r.toolObs) >= 256 {
		r.toolObs = map[string]toolObservation{}
	}
	r.toolObs[runID+"|"+toolName] = toolObservation{args: a, output: o}
	r.toolObsMu.Unlock()
}

// takeToolObserved 取出即删除（consume-once）；resume 重放工具消息时无 stash，自然降级。
func (r *EinoRunner) takeToolObserved(runID, toolName string) (toolObservation, bool) {
	r.toolObsMu.Lock()
	defer r.toolObsMu.Unlock()
	obs, ok := r.toolObs[runID+"|"+toolName]
	if ok {
		delete(r.toolObs, runID+"|"+toolName)
	}
	return obs, ok
}

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
	// maxOutputBytes 来自 run budget（0 = 不限制），透传给 executor 做全局截断。
	maxOutputBytes int
	// onDone 在 Execute 成功后回调（记录 args/output 摘要；失败/需审批不回调）。
	onDone func(args json.RawMessage, output string)
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
	var raw = json.RawMessage(argumentsInJSON)
	callID := g.def.Name + ":" + shortHash(itool.ArgsHash(raw))
	if g.approved != nil && g.approved[g.def.Name+":"+itool.ArgsHash(raw)] {
		out, err := g.executor.Execute(ctx, itool.ExecRequest{
			RunID: g.runID, ToolName: g.def.Name, Args: raw, Approved: true, ToolCallID: callID,
			MaxOutputBytes: g.maxOutputBytes,
		})
		if err != nil {
			return "", err
		}
		if g.onDone != nil {
			g.onDone(raw, out.Output)
		}
		return out.Output, nil
	}
	out, err := g.executor.Execute(ctx, itool.ExecRequest{
		RunID: g.runID, ToolName: g.def.Name, Args: raw, ToolCallID: callID,
		MaxOutputBytes: g.maxOutputBytes,
	})
	if err != nil {
		if errors.Is(err, domain.ErrNeedsApproval) && g.onNeedApproval != nil {
			g.onNeedApproval(g.runID, callID, g.def.Name, raw)
		}
		return "", err
	}
	if g.onDone != nil {
		g.onDone(raw, out.Output)
	}
	return out.Output, nil
}

func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

func (r *EinoRunner) buildAgent(ctx context.Context, runID string, budget domain.Budget, approved map[string]bool, memContext string) (*adk.ChatModelAgent, error) {
	return r.buildAgentFiltered(ctx, runID, budget, approved, nil, 0, memContext)
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
	agent, err := r.buildAgent(ctx, req.RunID, req.Budget, req.ApprovedTools, req.MemoryContext)
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
		r.emit(ctx, out, req.RunID, domain.EvtRunStarted, "run started", "", "")
		iter := runner.Run(ctx, []*schema.Message{schema.UserMessage(req.Goal)})
		r.drain(ctx, req.RunID, req.CheckpointID, iter, out, true)
	}()
	return out, nil
}

func (r *EinoRunner) executeResume(ctx context.Context, req ResumeRequest) (<-chan RuntimeEvent, error) {
	agent, err := r.buildAgent(ctx, req.RunID, resolveResumeBudget(req.Budget), req.ApprovedTools, "")
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
		r.emit(ctx, out, req.RunID, domain.EvtRunResumed, "run resumed", "", "")
		iter, err := runner.Resume(ctx, req.CheckpointID)
		if err != nil {
			r.emit(ctx, out, req.RunID, domain.EvtRunFailed, err.Error(), "", "")
			return
		}
		r.drain(ctx, req.RunID, req.CheckpointID, iter, out, true)
	}()
	return out, nil
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
