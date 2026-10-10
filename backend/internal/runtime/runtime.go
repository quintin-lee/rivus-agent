package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/observability"
	"rivus-agent-backend/internal/store"
	itool "rivus-agent-backend/internal/tool"
)

type Deps struct {
	Tasks     *store.TaskRepo
	Events    *store.EventRepo
	Approvals *store.ApprovalRepo
	Runner    *EinoRunner
}

type Runtime struct {
	deps Deps
	sem  chan struct{}
}

func New(deps Deps, concurrency int) *Runtime {
	if concurrency < 1 {
		concurrency = 1
	}
	rt := &Runtime{deps: deps, sem: make(chan struct{}, concurrency)}
	deps.Runner.OnEvent(func(ev RuntimeEvent) {
		payload, _ := json.Marshal(map[string]string{"text": ev.Text, "tool": ev.ToolName})
		_, _ = deps.Events.Append(context.Background(), ev.RunID, ev.Type, string(payload), "normal")
	})
	deps.Runner.OnNeedApproval(func(runID, toolCallID, toolName string, args json.RawMessage) {
		_ = runID
		_ = toolCallID
		_ = toolName
		_ = args
	})
	return rt
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
	_ = tracker
	deadline := time.Duration(budget.MaxDurationSeconds) * time.Second
	if deadline <= 0 {
		deadline = 600 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

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
		_ = r.deps.Approvals.Create(bg, &domain.Approval{
			RunID: runID, ToolCallID: toolCallID, ToolName: toolName,
			ArgsHash: itool.ArgsHash(args), RequestedBy: ownerID,
			Reason:    "high-risk tool call requires approval",
			ExpiresAt: time.Now().Add(30 * time.Minute).UnixMilli(),
		})
		r.append(bg, runID, domain.EvtApprovalRequested, toolName+" requires approval")
	})
	defer r.deps.Runner.SetNeedApprovalHandler(run.ID, nil)

	ch, err := r.deps.Runner.Run(ctx, RunRequest{
		RunID: run.ID, OwnerID: ownerID, Goal: run.Goal,
		Mode: mode, Budget: budget, ApprovedTools: approved,
		CheckpointID: fmt.Sprintf("ckpt_%s_%d", run.ID, time.Now().UnixNano()),
	})
	if err != nil {
		_ = r.deps.Tasks.UpdateStatus(ctx, ownerID, run.ID, domain.RunFailed, "runner_error", err.Error())
		observability.IncrRunFinished(false)
		return
	}
	toolEvidence := 0
	finalText := ""
	waitApproval := false
	for ev := range ch {
		switch ev.Type {
		case domain.EvtToolCompleted:
			toolEvidence++
		case domain.EvtApprovalRequested:
			waitApproval = true
		case domain.EvtModelCompleted:
			finalText = ev.Text
		case domain.EvtRunFailed:
			finalText = ev.Text
		}
	}
	if waitApproval || needApproval.Load() {
		_ = r.deps.Tasks.UpdateStatus(ctx, ownerID, run.ID, domain.RunWaitingApproval, "", "")
		observability.IncrRunFinished(false)
		return
	}
	report := Verify(run.SuccessCriteria, finalText, toolEvidence)
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
	_, _ = r.deps.Events.Append(ctx, runID, typ, string(payload), "normal")
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
