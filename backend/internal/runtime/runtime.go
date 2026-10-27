package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/observability"
	"rivus-agent-backend/internal/store"
	itool "rivus-agent-backend/internal/tool"

	"github.com/google/uuid"
)

type Deps struct {
	Tasks     *store.TaskRepo
	Events    *store.EventRepo
	Approvals *store.ApprovalRepo
	Runner    *EinoRunner
	Log       *slog.Logger      // 可空：缺省 slog.Default()
	Memories  *store.MemoryRepo // 可空：nil 则顶层 run 不注入会话记忆
}

type Runtime struct {
	deps Deps
	sem  chan struct{}
}

func New(deps Deps, concurrency int) *Runtime {
	if concurrency < 1 {
		concurrency = 1
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	rt := &Runtime{deps: deps, sem: make(chan struct{}, concurrency)}
	deps.Runner.OnEvent(func(ev RuntimeEvent) {
		payload := mergeEventPayload(ev.Text, ev.ToolName, ev.Payload)
		if _, err := deps.Events.Append(context.Background(), ev.RunID, ev.Type, payload, "normal"); err != nil {
			deps.Log.Warn("event append failed", "run_id", ev.RunID, "type", ev.Type, "err", err)
			observability.IncrEventAppendFailed()
		}
	})
	deps.Runner.OnNeedApproval(func(runID, toolCallID, toolName string, args json.RawMessage) {
		_ = runID
		_ = toolCallID
		_ = toolName
		_ = args
	})
	return rt
}

// mergeEventPayload 合并基础 {"text","tool"} 与 extra JSON（extra 键胜出；非法 extra 忽略）。
// 现有全部 emit 调用 extra 均为空 → 输出与历史字节一致。
func mergeEventPayload(text, tool, extra string) string {
	m := map[string]string{"text": text, "tool": tool}
	if extra != "" {
		var e map[string]string
		if err := json.Unmarshal([]byte(extra), &e); err == nil {
			for k, v := range e {
				m[k] = v
			}
		}
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func (r *Runtime) Execute(ctx context.Context, ownerID string, run *domain.Run, approved map[string]bool) {
	select {
	case r.sem <- struct{}{}:
	case <-ctx.Done():
		return
	}
	go func() {
		defer func() { <-r.sem }()
		r.run(ctx, ownerID, run, approved)
	}()
}

func (r *Runtime) run(ctx context.Context, ownerID string, run *domain.Run, approved map[string]bool) {
	observability.IncrRunStarted()
	_ = r.deps.Tasks.MarkStarted(ctx, ownerID, run.ID)
	budget := run.Budget
	tracker := NewBudgetTracker(budget)
	deadline := time.Duration(budget.MaxDurationSeconds) * time.Second
	if deadline <= 0 {
		deadline = 600 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	// 顶层注入 depth-0 TaskContext（保 values 流入 runner.Run → 每个 task 工具调用可见）
	ctx = WithTaskContext(ctx, TaskContext{OwnerID: ownerID, ParentRunID: run.ID,
		SessionID: run.SessionID, ParentBudget: run.Budget, Depth: 0})

	mode := SelectMode(run.Mode, run.Goal)
	if mode == "plan_execute" {
		steps := BuildInitialPlan(run.Goal, run.SuccessCriteria)
		dsteps := make([]domain.Step, 0, len(steps))
		for _, s := range steps {
			dsteps = append(dsteps, domain.Step{
				ID: uuid.NewString(), RunID: run.ID, Index: s.Index,
				Description: s.Description, Status: domain.StepPending, Dependencies: s.DependsOn,
			})
		}
		_ = r.deps.Tasks.CreateSteps(ctx, run.ID, dsteps)
		r.append(ctx, run.ID, domain.EvtPlanCreated, "plan with "+itoa(len(dsteps))+" steps")
	}

	var needApproval atomic.Bool
	r.deps.Runner.SetNeedApprovalHandler(run.ID, func(runID, toolCallID, toolName string, args json.RawMessage) {
		needApproval.Store(true)
		bg, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := r.deps.Approvals.FindPendingByToolCall(bg, runID, toolCallID); err == nil {
			return
		}
		a := &domain.Approval{
			RunID: runID, ToolCallID: toolCallID, ToolName: toolName,
			ArgsHash: itool.ArgsHash(args), RequestedBy: ownerID,
			Reason:    "high-risk tool call requires approval",
			ExpiresAt: time.Now().Add(30 * time.Minute).UnixMilli(),
		}
		if err := r.deps.Approvals.Create(bg, a); err == nil {
			r.appendApproval(bg, runID, a)
		}
	})
	defer r.deps.Runner.SetNeedApprovalHandler(run.ID, nil)

	ch, err := r.deps.Runner.Run(ctx, RunRequest{
		RunID: run.ID, OwnerID: ownerID, Goal: run.Goal,
		Mode: mode, Budget: budget, ApprovedTools: approved,
		SessionID: run.SessionID, MemoryContext: r.memoryBlock(ctx, run.SessionID),
		CheckpointID: fmt.Sprintf("ckpt_%s_%d", run.ID, time.Now().UnixNano()),
	})
	if err != nil {
		_ = r.deps.Tasks.UpdateStatus(ctx, ownerID, run.ID, domain.RunFailed, "runner_error", err.Error())
		observability.IncrRunFinished(false)
		return
	}
	started := time.Now()
	finalText := ""
	waitApproval := false
	modelFailed := false
	budgetFired := false
	budgetCode := ""
	for ev := range ch {
		switch ev.Type {
		case domain.EvtToolCompleted:
			tracker.AddToolCall()
			r.appendBudget(ctx, run.ID, tracker, started)
		case domain.EvtApprovalRequested:
			waitApproval = true
		case domain.EvtModelCompleted:
			finalText = ev.Text
			tracker.AddModelCall()
			tracker.AddOutput(len(ev.Text))
			r.appendBudget(ctx, run.ID, tracker, started)
		case domain.EvtModelFailed:
			modelFailed = true
		case domain.EvtRunFailed:
			finalText = ev.Text
		}
		if code, hit := tracker.OverBudget(); hit && !budgetFired {
			budgetFired = true
			budgetCode = code
			_ = r.deps.Runner.Cancel(run.ID)
		}
	}
	u := tracker.Snapshot()
	if waitApproval || needApproval.Load() {
		_ = r.deps.Tasks.UpdateStatus(ctx, ownerID, run.ID, domain.RunWaitingApproval, "", "")
		observability.IncrRunFinished(false)
		return
	}
	if budgetCode != "" {
		// Cancel 已让 runner 退出；写库统一走 bgCtx（ctx 可能已随 cancel/timeout 失效，
		// database/sql 对死 ctx 的写会静默 no-op）。
		bgCtx := context.Background()
		msg := "budget exceeded: " + budgetCode
		_ = r.deps.Tasks.UpdateStatus(bgCtx, ownerID, run.ID, domain.RunFailed, budgetCode, msg)
		r.append(bgCtx, run.ID, domain.EvtBudgetExceeded, msg)
		observability.IncrRunFinished(false)
		r.deps.Runner.DeleteCheckpointsByRun(bgCtx, run.ID)
		return
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		// ctx is DEAD here: database/sql short-circuits writes on a dead context
		// (prepareContext returns ctx.Err() without touching the driver). All writes
		// in this branch must use context.Background() or the status update / event
		// append silently no-op and the run stays `running` forever.
		bgCtx := context.Background()
		_ = r.deps.Tasks.UpdateStatus(bgCtx, ownerID, run.ID, domain.RunTimedOut, "timed_out", "run exceeded max duration budget")
		r.append(bgCtx, run.ID, domain.EvtRunTimedOut, "run timed out")
		observability.IncrRunFinished(false)
		r.deps.Runner.DeleteCheckpointsByRun(bgCtx, run.ID)
		return
	}
	if modelFailed && u.ToolCalls == 0 {
		_ = r.deps.Tasks.UpdateStatus(ctx, ownerID, run.ID, domain.RunFailed, "model_failed", "model request failed and no tool evidence was produced")
		r.append(ctx, run.ID, domain.EvtVerifyFailed, "model failed")
		observability.IncrRunFinished(false)
		r.deps.Runner.DeleteCheckpointsByRun(context.Background(), run.ID)
		return
	}
	if budget.MaxOutputBytes > 0 && len(finalText) > budget.MaxOutputBytes {
		finalText = finalText[:budget.MaxOutputBytes] + "...[truncated]"
	}
	report := Verify(run.SuccessCriteria, finalText, u.ToolCalls)
	if report.Passed && len(run.SuccessCriteria) == 0 && u.ToolCalls == 0 && finalText == "" {
		report.Passed = false
		report.Missing = []string{"empty result: no model output and no tool evidence"}
	}
	rb, _ := json.Marshal(report)
	cleanup := func() { r.deps.Runner.DeleteCheckpointsByRun(context.Background(), run.ID) }
	if report.Passed {
		_ = r.deps.Tasks.SetResult(ctx, run.ID, string(rb))
		r.append(ctx, run.ID, domain.EvtVerifyPassed, "verification passed")
		_ = r.deps.Tasks.UpdateStatus(ctx, ownerID, run.ID, domain.RunSucceeded, "", "")
		observability.IncrRunFinished(true)
		cleanup()
	} else {
		_ = r.deps.Tasks.SetResult(ctx, run.ID, string(rb))
		r.append(ctx, run.ID, domain.EvtVerifyFailed, "verification failed")
		_ = r.deps.Tasks.UpdateStatus(ctx, ownerID, run.ID, domain.RunFailed, "verification_failed", "success criteria not met")
		observability.IncrRunFinished(false)
		cleanup()
	}
}

func (r *Runtime) append(ctx context.Context, runID string, typ domain.EventType, text string) {
	payload, _ := json.Marshal(map[string]string{"text": text})
	if _, err := r.deps.Events.Append(ctx, runID, typ, string(payload), "normal"); err != nil {
		r.deps.Log.Warn("event append failed", "run_id", runID, "type", typ, "err", err)
		observability.IncrEventAppendFailed()
	}
}

// appendBudget 在 tool/model 步骤边界发 budget.consumed(累计
// model_calls/tool_calls/duration_seconds)。payload 为结构化 JSON,故直接走
// Events.Append(与 append 同一持久化 + eventshub 推送路径)。单 attempt 内
// 计数从 0 单调递增;跨 attempt(Resume/Retry)重起(有意,见 spec)。
func (r *Runtime) appendBudget(ctx context.Context, runID string, tracker *BudgetTracker, started time.Time) {
	u := tracker.Snapshot()
	payload, _ := json.Marshal(map[string]int{
		"model_calls":      u.ModelCalls,
		"tool_calls":       u.ToolCalls,
		"duration_seconds": int(time.Since(started).Seconds()),
	})
	if _, err := r.deps.Events.Append(ctx, runID, domain.EvtBudgetConsumed, string(payload), "normal"); err != nil {
		r.deps.Log.Warn("event append failed", "run_id", runID, "type", domain.EvtBudgetConsumed, "err", err)
		observability.IncrEventAppendFailed()
	}
}

// appendApproval 发带 approval_id + tool_name 的 approval.requested 事件。
func (r *Runtime) appendApproval(ctx context.Context, runID string, a *domain.Approval) {
	payload, _ := json.Marshal(map[string]string{
		"text":        a.ToolName + " requires approval",
		"approval_id": a.ID,
		"tool_name":   a.ToolName,
	})
	if _, err := r.deps.Events.Append(ctx, runID, domain.EvtApprovalRequested, string(payload), "normal"); err != nil {
		r.deps.Log.Warn("event append failed", "run_id", runID, "type", domain.EvtApprovalRequested, "err", err)
		observability.IncrEventAppendFailed()
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	b := []byte{}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

const (
	memoryInjectMax     = 10       // 注入最近条数
	memoryBlockMaxBytes = 8 * 1024 // 记忆块总字节上限
)

// memoryBlock 取会话最近记忆并格式化为指令块；无 repo / 无会话 / 无记忆 / 出错 → ""。
func (r *Runtime) memoryBlock(ctx context.Context, sessionID string) string {
	if r.deps.Memories == nil || sessionID == "" {
		return ""
	}
	mems, err := r.deps.Memories.Latest(ctx, sessionID, memoryInjectMax)
	if err != nil || len(mems) == 0 {
		return ""
	}
	return composeMemoryBlock(mems)
}

func composeMemoryBlock(mems []domain.Memory) string {
	if len(mems) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n\n## 会话记忆（此前沉淀，供参考）\n")
	total := 0
	for _, m := range mems {
		line := "- " + m.Content + "\n"
		if total+len(line) > memoryBlockMaxBytes {
			break
		}
		sb.WriteString(line)
		total += len(line)
	}
	return sb.String()
}
