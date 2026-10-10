package service

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"rivus-agent-backend/internal/config"
	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/runtime"
	"rivus-agent-backend/internal/store"
)

type RunService struct {
	cfg       config.Config
	tasks     *store.TaskRepo
	events    *store.EventRepo
	approvals *store.ApprovalRepo
	rt        *runtime.Runtime
	runner    *runtime.EinoRunner
}

func NewRunService(cfg config.Config, tasks *store.TaskRepo, events *store.EventRepo, approvals *store.ApprovalRepo, rt *runtime.Runtime, runner *runtime.EinoRunner) *RunService {
	return &RunService{cfg: cfg, tasks: tasks, events: events, approvals: approvals, rt: rt, runner: runner}
}

type CreateRunInput struct {
	OwnerID        string
	SessionID      string
	Spec           domain.TaskSpec
	Budget         domain.Budget
	IdempotencyKey string
}

func (s *RunService) CreateRun(ctx context.Context, in CreateRunInput) (*domain.Run, bool, error) {
	if in.Spec.Goal == "" {
		return nil, false, domain.ErrBadRequest
	}
	if in.SessionID == "" || in.OwnerID == "" {
		return nil, false, domain.ErrBadRequest
	}
	if _, _, _, _, err := s.tasks.GetSession(ctx, in.OwnerID, in.SessionID); err != nil {
		if err == sql.ErrNoRows {
			return nil, false, domain.ErrBadRequest
		}
		return nil, false, err
	}
	if in.IdempotencyKey != "" {
		if existing, err := s.tasks.FindByIdempotencyKey(ctx, in.OwnerID, in.IdempotencyKey); err == nil {
			return existing, true, nil
		}
	}
	serverMax := domain.Budget{
		MaxDurationSeconds: s.cfg.MaxDurationS, MaxModelCalls: s.cfg.MaxModelCalls,
		MaxToolCalls: s.cfg.MaxToolCalls, MaxIterations: s.cfg.MaxIterations,
		MaxOutputBytes: s.cfg.MaxOutputBytes,
	}
	budget := in.Budget.ClampByServer(serverMax)
	mode := in.Spec.Mode
	if mode == "" {
		mode = "react"
	}
	run := &domain.Run{
		ID: "run_" + uuid.NewString(), SessionID: in.SessionID, OwnerID: in.OwnerID,
		Status: domain.RunQueued, Mode: mode, Goal: in.Spec.Goal,
		Constraints: in.Spec.Constraints, SuccessCriteria: in.Spec.SuccessCriteria,
		Budget: budget, Attempt: 1, IdempotencyKey: in.IdempotencyKey,
	}
	run.CheckpointID = "ckpt_" + run.ID
	if err := s.tasks.CreateRun(ctx, run); err != nil {
		return nil, false, err
	}
	_, _ = s.events.Append(ctx, run.ID, domain.EvtRunCreated, `{"goal":"created"}`, "normal")
	s.rt.Execute(context.WithoutCancel(ctx), in.OwnerID, run, map[string]bool{})
	return run, false, nil
}

func (s *RunService) Cancel(ctx context.Context, ownerID, runID string) error {
	run, err := s.tasks.GetRun(ctx, ownerID, runID)
	if err != nil {
		return err
	}
	_ = s.runner.Cancel(runID)
	if run.Status.Terminal() {
		return domain.ErrConflict
	}
	return s.tasks.UpdateStatus(ctx, ownerID, runID, domain.RunCancelled, "cancelled", "cancelled by user")
}

func (s *RunService) Resume(ctx context.Context, ownerID, runID string) error {
	run, err := s.tasks.GetRun(ctx, ownerID, runID)
	if err != nil {
		return err
	}
	switch run.Status {
	case domain.RunPaused, domain.RunWaitingApproval:
		if err := s.tasks.UpdateStatus(ctx, ownerID, runID, domain.RunRunning, "", ""); err != nil {
			return err
		}
	case domain.RunFailed:
		if err := s.tasks.RetryFailed(ctx, ownerID, runID); err != nil {
			return err
		}
		run.Attempt++
		_, _ = s.events.Append(ctx, runID, domain.EvtRunResumed, fmt.Sprintf(`{"attempt":%d}`, run.Attempt), "normal")
	default:
		return domain.ErrConflict
	}
	s.rt.Execute(context.WithoutCancel(ctx), ownerID, run, s.collectApprovals(ctx, runID))
	return nil
}

func (s *RunService) collectApprovals(ctx context.Context, runID string) map[string]bool {
	out := map[string]bool{}
	records, err := s.approvals.ListByRun(ctx, runID)
	if err != nil {
		return out
	}
	now := time.Now().UnixMilli()
	for _, a := range records {
		if a.Status == domain.ApprovalApproved && now <= a.ExpiresAt {
			out[a.ToolName+":"+a.ArgsHash] = true
		}
	}
	return out
}

type DecideApprovalInput struct {
	OwnerID    string
	RunID      string
	ApprovalID string
	Approve    bool
	Reason     string
}

func (s *RunService) DecideApproval(ctx context.Context, in DecideApprovalInput) error {
	run, err := s.tasks.GetRun(ctx, in.OwnerID, in.RunID)
	if err != nil {
		return err
	}
	_ = run
	a, err := s.approvals.Get(ctx, in.ApprovalID)
	if err != nil {
		return err
	}
	if a.RunID != in.RunID {
		return domain.ErrForbidden
	}
	if time.Now().UnixMilli() > a.ExpiresAt {
		_ = s.approvals.Decide(ctx, in.ApprovalID, false, in.OwnerID, "expired")
		return domain.Wrap("approval_expired", "approval expired", false, nil)
	}
	if err := s.approvals.Decide(ctx, in.ApprovalID, in.Approve, in.OwnerID, in.Reason); err != nil {
		return err
	}
	_, _ = s.events.Append(ctx, in.RunID, domain.EvtApprovalDecided, `{"approved":`+boolStr(in.Approve)+`}`, "normal")
	if in.Approve {
		return s.Resume(ctx, in.OwnerID, in.RunID)
	}
	_ = s.tasks.UpdateStatus(ctx, in.OwnerID, in.RunID, domain.RunFailed, "approval_rejected", "approval rejected by "+in.OwnerID)
	return nil
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
