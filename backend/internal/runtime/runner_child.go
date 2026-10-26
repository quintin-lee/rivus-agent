package runtime

import (
	"context"
	"errors"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"rivus-agent-backend/internal/domain"
	itool "rivus-agent-backend/internal/tool"
)

// ChildRequest 驱动一个只读子 agent（模型循环）；DB 终态化由 subagent handler 负责。
type ChildRequest struct {
	OwnerID, ChildRunID string
	Goal                string
	ToolNames           []string // 父选的只读子集；nil=全量只读
	Budget              domain.Budget
	Depth               int // 1=子
	CheckpointID        string
}

// ChildOutcome 是 RunChild 的分类结果（不含 DB 写）。
type ChildOutcome struct {
	FinalText    string
	ToolEvidence int
	ModelFailed  bool
	SawApproval  bool
	Cancelled    bool
	Deadline     bool
	PreDrainErr  error
}

// ChildExecutor 由 *EinoRunner 实现（subagent 包依赖此接口，避免 import 具体实现细节）。
type ChildExecutor interface {
	RunChild(ctx context.Context, req ChildRequest) ChildOutcome
}

// CheckpointCleaner 清子 checkpoint（*EinoRunner 已实现 DeleteCheckpointsByRun，runner_eino.go:108）。
// subagent 经此接口引用，避免 import 具体类型之外的依赖。
type CheckpointCleaner interface {
	DeleteCheckpointsByRun(ctx context.Context, runID string)
}

// filterToolNames：toolNames nil→**保持 nil**（全量由 buildAgentFiltered 处理）；否则只保留命中项；depth>=1 剔除 "task"。
func filterToolNames(toolNames []string, depth int) []string {
	if toolNames == nil {
		return nil
	}
	out := make([]string, 0, len(toolNames))
	for _, n := range toolNames {
		if depth >= 1 && n == "task" {
			continue
		}
		out = append(out, n)
	}
	return out
}

// buildAgentFiltered：按 toolNames(+depth 守卫)过滤工具，构建子 agent。
func (r *EinoRunner) buildAgentFiltered(ctx context.Context, runID string, budget domain.Budget,
	approved map[string]bool, toolNames []string, depth int) (*adk.ChatModelAgent, error) {
	base, err := r.modelFactory()
	if err != nil {
		return nil, err
	}
	names := filterToolNames(toolNames, depth) // 注意：nil 表示全量
	var defs []itool.Definition
	if names == nil {
		defs = r.registry.List()
	} else {
		for _, n := range names {
			if d, ok := r.registry.Get(n); ok {
				defs = append(defs, d)
			}
		}
	}
	// 关键：depth>=1 时，全量分支也要剔除 task（父选的 nil 不应让子拿到 task）。用新切片，避免 in-place alias。
	if depth >= 1 {
		kept := make([]itool.Definition, 0, len(defs))
		for _, d := range defs {
			if d.Name != "task" {
				kept = append(kept, d)
			}
		}
		defs = kept
	}
	var tools []tool.BaseTool
	for _, d := range defs {
		d := d
		tools = append(tools, &gatewayTool{def: d, runID: runID, approved: approved,
			executor: r.executor, onNeedApproval: r.needApprovalHandler(runID),
			maxOutputBytes: budget.MaxOutputBytes})
	}
	withTools, err := base.WithTools(collectInfos(tools))
	if err != nil {
		return nil, err
	}
	maxIter := budget.MaxIterations
	if maxIter <= 0 {
		maxIter = 12
	}
	r.mu.RLock()
	instr := r.instruction
	r.mu.RUnlock()
	cfg := &adk.ChatModelAgentConfig{Name: "rivus", Description: "general purpose agent",
		Instruction: instr, Model: withTools, MaxIterations: maxIter}
	cfg.ToolsConfig.Tools = tools
	return adk.NewChatModelAgent(ctx, cfg)
}

// RunChild：同步驱动一个只读子 agent（复用父 ctx 做 cancel/deadline 级联；不碰 r.sem）。
// 只做模型循环 + 分类，DB 终态化交给调用方（subagent handler）。
func (r *EinoRunner) RunChild(ctx context.Context, req ChildRequest) ChildOutcome {
	agent, err := r.buildAgentFiltered(ctx, req.ChildRunID, req.Budget, nil, req.ToolNames, req.Depth)
	if err != nil {
		return ChildOutcome{PreDrainErr: err}
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent, EnableStreaming: true, CheckPointStore: r.checkpoints})
	out := make(chan RuntimeEvent, 64)
	r.emit(ctx, out, req.ChildRunID, domain.EvtRunStarted, "child run started", "", "") // 子事件含 run.started（经 OnEvent 免费落库）
	iter := runner.Run(ctx, []*schema.Message{schema.UserMessage(req.Goal)})
	go func() { defer close(out); r.drain(ctx, req.ChildRunID, req.CheckpointID, iter, out, false) }()

	var o ChildOutcome
	for ev := range out {
		switch ev.Type {
		case domain.EvtToolCompleted:
			o.ToolEvidence++
		case domain.EvtModelCompleted:
			o.FinalText = ev.Text
		case domain.EvtModelFailed:
			o.ModelFailed = true
		case domain.EvtRunFinished:
			o.FinalText = ev.Text
		case domain.EvtApprovalRequested:
			o.SawApproval = true
		}
	}
	if o.SawApproval {
		return o
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		o.Deadline = true
		return o
	}
	if ctx.Err() == context.Canceled {
		o.Cancelled = true
		return o
	}
	return o
}
