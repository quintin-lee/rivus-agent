package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/observability"
	"rivus-agent-backend/internal/runtime"
	itool "rivus-agent-backend/internal/tool"
	"rivus-agent-backend/internal/store"
)

const taskToolName = "task"

// TaskTool 返回 task 工具的 Definition（ApprovalNever；risk 由子的只读面界定为 read）。
// runner 驱动子模型循环（ChildExecutor）；cleaner 清子 checkpoint（CheckpointCleaner）。
// 装配处二者通常传同一个 *EinoRunner（它同时实现两个接口）。
func TaskTool(reg *itool.Registry, tasks *store.TaskRepo, runner runtime.ChildExecutor, cleaner runtime.CheckpointCleaner) itool.Definition {
	h := NewTaskHandler(reg, tasks, runner, cleaner)
	return itool.Definition{
		Name: taskToolName,
		Description: "Delegate a read-only sub-goal to a child agent; runs synchronously and returns its result.",
		Version: "v1",
		InputSchema: `{"type":"object","properties":{"goal":{"type":"string"},"tools":{"type":"array","items":{"type":"string"}},"max_model_calls":{"type":"integer"},"max_tool_calls":{"type":"integer"},"max_iterations":{"type":"integer"}},"required":["goal"]}`,
		Risk: itool.RiskRead, Approval: itool.ApprovalNever, Idempotent: false, TimeoutMs: 120000, MaxResultBytes: 64 * 1024,
		Handler: h,
	}
}

type taskIn struct {
	Goal          string   `json:"goal"`
	Tools         []string `json:"tools"`
	MaxModelCalls *int     `json:"max_model_calls"`
	MaxToolCalls  *int     `json:"max_tool_calls"`
	MaxIterations *int     `json:"max_iterations"`
}

// NewTaskHandler：task 工具的唯一执行入口（父身份/预算/深度经 ctx）。
func NewTaskHandler(reg *itool.Registry, tasks *store.TaskRepo, runner runtime.ChildExecutor, cleaner runtime.CheckpointCleaner) itool.Handler {
	deps := childDeps{reg: reg, tasks: tasks, runner: runner, cleaner: cleaner}
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		tc, ok := runtime.TaskContextFrom(ctx)
		if !ok || tc.Depth >= 1 {
			return errRes("task not available at this depth").String(), nil
		}
		var in taskIn
		if err := json.Unmarshal(args, &in); err != nil || strings.TrimSpace(in.Goal) == "" {
			return errRes("task: invalid goal").String(), nil
		}
		return runOneChild(ctx, deps, tc, in).String(), nil
	}
}

// childDeps：子委派所需的只读依赖集（reg/tasks/runner/cleaner）。
type childDeps struct {
	reg     *itool.Registry
	tasks   *store.TaskRepo
	runner  runtime.ChildExecutor
	cleaner runtime.CheckpointCleaner
}

// runOneChild：单个子目标的完整生命周期（白名单→clamp→建行→模型循环→分类→终态化）。
// ctx 必须是父活 ctx（cancel/deadline 级联）；DB 写全部走 bg（dead-ctx-safe）。
func runOneChild(ctx context.Context, deps childDeps, tc runtime.TaskContext, in taskIn) taskResult {
	toolNames, err := resolveReadOnlyTools(deps.reg, in.Tools)
	if err != nil {
		return errRes(err.Error())
	}
	budget := clampChildBudget(tc.ParentBudget, in)
	// 子行落库 + 所有子 DB 写走 context.Background()（dead-ctx-safe；父 cancel/deadline 时 ctx 已死）
	bg := context.Background()
	childID := uuid.NewString()
	if err := deps.tasks.CreateRun(bg, &domain.Run{
		ID: childID, OwnerID: tc.OwnerID, SessionID: tc.SessionID,
		Status: domain.RunQueued, Mode: "react", Goal: in.Goal,
		Budget: budget, ParentRunID: tc.ParentRunID, Attempt: 1,
	}); err != nil {
		return errRes("task: create child run: " + err.Error())
	}
	_ = deps.tasks.MarkStarted(bg, tc.OwnerID, childID)
	observability.IncrRunStarted()
	// 模型循环用活 ctx（父 ctx）：父 cancel/deadline 经 drain 级联到子。
	o := deps.runner.RunChild(ctx, runtime.ChildRequest{
		OwnerID: tc.OwnerID, ChildRunID: childID, Goal: in.Goal,
		ToolNames: toolNames, Budget: budget, Depth: tc.Depth + 1,
		CheckpointID: "ckpt_" + childID + "_" + fmt.Sprintf("%d", time.Now().UnixNano()),
	})
	switch {
	case o.PreDrainErr != nil:
		finalizeChild(bg, deps.tasks, tc.OwnerID, childID, domain.RunFailed, "runner_error", o.PreDrainErr.Error(), false, deps.cleaner)
		return taskResult{RunID: childID, Status: "failed", Error: "runner_error: " + o.PreDrainErr.Error()}
	case o.SawApproval:
		finalizeChild(bg, deps.tasks, tc.OwnerID, childID, domain.RunFailed, "child_needs_approval", "", false, deps.cleaner)
		return taskResult{RunID: childID, Status: "failed", Error: "child hit approval (unsupported for read-only child in v1)"}
	case o.Deadline:
		finalizeChild(bg, deps.tasks, tc.OwnerID, childID, domain.RunTimedOut, "timed_out", "child exceeded budget", false, deps.cleaner)
		return taskResult{RunID: childID, Status: "timed_out", Text: o.FinalText}
	case o.Cancelled:
		finalizeChild(bg, deps.tasks, tc.OwnerID, childID, domain.RunCancelled, "cancelled", "parent cancelled", false, deps.cleaner)
		return taskResult{RunID: childID, Status: "cancelled", Text: o.FinalText, Error: "cancelled"}
	case o.ModelFailed && o.ToolEvidence == 0:
		finalizeChild(bg, deps.tasks, tc.OwnerID, childID, domain.RunFailed, "model_failed", "child model failed, no tool evidence", false, deps.cleaner)
		return taskResult{RunID: childID, Status: "failed", Error: "model_failed"}
	case strings.TrimSpace(o.FinalText) == "" && o.ToolEvidence == 0:
		// 空结果守卫：无文本且无工具证据 → 失败，绝不判 succeeded。
		finalizeChild(bg, deps.tasks, tc.OwnerID, childID, domain.RunFailed, "empty_result", "child produced no result or evidence", false, deps.cleaner)
		return taskResult{RunID: childID, Status: "failed", Error: "empty_result"}
	default:
		rep := runtime.Verify(nil, o.FinalText, o.ToolEvidence) // 无 success_criteria → Passed 恒真
		rb, _ := json.Marshal(map[string]any{"passed": rep.Passed, "evidence": o.FinalText})
		_ = deps.tasks.SetResult(bg, childID, string(rb))
		if rep.Passed {
			finalizeChild(bg, deps.tasks, tc.OwnerID, childID, domain.RunSucceeded, "", "", true, deps.cleaner)
		} else {
			finalizeChild(bg, deps.tasks, tc.OwnerID, childID, domain.RunFailed, "verification_failed", "", false, deps.cleaner)
		}
		return taskResult{RunID: childID, Status: stringOfStatus(rep.Passed), Text: o.FinalText}
	}
}

// finalizeChild：子行终态化 + 记 metric + 清 checkpoint（全 bg，dead-ctx-safe）。
func finalizeChild(bg context.Context, tasks *store.TaskRepo, owner, id string, to domain.RunStatus, code, msg string, passed bool, cleaner runtime.CheckpointCleaner) {
	_ = tasks.UpdateStatus(bg, owner, id, to, code, msg)
	observability.IncrRunFinished(passed)
	cleaner.DeleteCheckpointsByRun(bg, id)
}

// 结果 JSON 小助手（回传给父模型）。
type taskResult struct {
	RunID  string `json:"run_id"`
	Status string `json:"status"`
	Text   string `json:"text"`
	Error  string `json:"error,omitempty"`
}

func out(runID, status, text, errMsg string) *taskResult {
	return &taskResult{RunID: runID, Status: status, Text: text, Error: errMsg}
}

func (r taskResult) String() string { b, _ := json.Marshal(r); return string(b) }

// errRes：统一失败结果（等价旧 errOut 的 JSON 超集：多空 run_id/text 键）。
func errRes(msg string) taskResult { return taskResult{Status: "failed", Error: msg} }

func stringOfStatus(passed bool) string {
	if passed {
		return "succeeded"
	}
	return "failed"
}

// resolveReadOnlyTools：names 缺省 = registry 里全部 ApprovalNever+RiskRead（含 task 本身；depth 守卫会剔除）。
// 否则每个 name 必须在 registry 且 ApprovalNever+RiskRead，否则报错。
func resolveReadOnlyTools(reg *itool.Registry, names []string) ([]string, error) {
	if len(names) == 0 {
		var out []string
		for _, d := range reg.List() {
			if d.Approval == itool.ApprovalNever && d.Risk == itool.RiskRead {
				out = append(out, d.Name)
			}
		}
		return out, nil
	}
	res := make([]string, 0, len(names))
	for _, n := range names {
		d, ok := reg.Get(n)
		if !ok {
			return nil, fmt.Errorf("unknown tool %q", n)
		}
		if !(d.Approval == itool.ApprovalNever && d.Risk == itool.RiskRead) {
			return nil, fmt.Errorf("tool %q is not a read-only (ApprovalNever+RiskRead) tool", n)
		}
		res = append(res, n)
	}
	return res, nil
}

const (
	childBudgetFrac         = 0.40
	childCapModelCalls      = 20
	childCapToolCalls       = 30
	childCapIterations      = 10
	childCapDurationSeconds = 180
	childCapOutputBytes     = 8 * 1024
)

// clampChildBudget：子预算 = 父的 40%（收紧不放宽），再与请求值取 min，且不得超过命名上限。
func clampChildBudget(parent domain.Budget, in taskIn) domain.Budget {
	frac := func(v int) int {
		if v <= 0 {
			return 0
		}
		return int(float64(v) * childBudgetFrac)
	}
	capTo := func(v, cap int) int { if v > cap { return cap }; return v }
	mmc := capTo(frac(parent.MaxModelCalls), childCapModelCalls)
	mtc := capTo(frac(parent.MaxToolCalls), childCapToolCalls)
	mi := capTo(frac(parent.MaxIterations), childCapIterations)
	md := capTo(frac(parent.MaxDurationSeconds), childCapDurationSeconds)
	mo := capTo(frac(parent.MaxOutputBytes), childCapOutputBytes)
	if in.MaxModelCalls != nil {
		mmc = capTo(*in.MaxModelCalls, childCapModelCalls)
	}
	if in.MaxToolCalls != nil {
		mtc = capTo(*in.MaxToolCalls, childCapToolCalls)
	}
	if in.MaxIterations != nil {
		mi = capTo(*in.MaxIterations, childCapIterations)
	}
	// 缺省(请求 0)则取 40%；请求值则收紧到 min(请求, 上限)
	return domain.Budget{MaxModelCalls: mmc, MaxToolCalls: mtc, MaxIterations: mi,
		MaxDurationSeconds: md, MaxOutputBytes: mo}
}
