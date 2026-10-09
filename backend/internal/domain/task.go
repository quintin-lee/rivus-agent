package domain

// RunStatus 是 Run 状态机状态。
type RunStatus string

const (
	RunQueued          RunStatus = "queued"
	RunRunning         RunStatus = "running"
	RunWaitingApproval RunStatus = "waiting_approval"
	RunPaused          RunStatus = "paused"
	RunSucceeded       RunStatus = "succeeded"
	RunFailed          RunStatus = "failed"
	RunCancelled       RunStatus = "cancelled"
	RunTimedOut        RunStatus = "timed_out"
)

// Terminal reports whether s is a terminal state.
func (s RunStatus) Terminal() bool {
	switch s {
	case RunSucceeded, RunFailed, RunCancelled, RunTimedOut:
		return true
	default:
		return false
	}
}

// ValidTransition 控制合法状态转换，终态不可直接改回 running。
func ValidTransition(from, to RunStatus) bool {
	if from.Terminal() {
		return false
	}
	switch from {
	case RunQueued:
		return to == RunRunning || to == RunCancelled
	case RunRunning:
		switch to {
		case RunWaitingApproval, RunPaused, RunSucceeded, RunFailed, RunCancelled, RunTimedOut:
			return true
		}
	case RunWaitingApproval:
		return to == RunRunning || to == RunCancelled || to == RunFailed
	case RunPaused:
		return to == RunRunning || to == RunCancelled
	}
	return false
}

// StepStatus 是 Plan-Execute 步骤状态。
type StepStatus string

const (
	StepPending          StepStatus = "pending"
	StepRunning          StepStatus = "running"
	StepSucceeded        StepStatus = "succeeded"
	StepFailed           StepStatus = "failed"
	StepSkipped          StepStatus = "skipped"
	StepAwaitingApproval StepStatus = "awaiting_approval"
)

// TaskSpec 是创建 Run 的业务输入。
type TaskSpec struct {
	Goal            string            `json:"goal"`
	Constraints     []string          `json:"constraints,omitempty"`
	SuccessCriteria []string          `json:"success_criteria,omitempty"`
	Mode            string            `json:"mode,omitempty"` // react | plan_execute
	Metadata        map[string]string `json:"metadata,omitempty"`
}

// Budget 是单 Run 资源预算。
type Budget struct {
	MaxDurationSeconds int `json:"max_duration_seconds"`
	MaxModelCalls      int `json:"max_model_calls"`
	MaxToolCalls       int `json:"max_tool_calls"`
	MaxIterations      int `json:"max_iterations"`
	MaxOutputBytes     int `json:"max_output_bytes"`
}

// ClampByServer 用服务端上限收紧客户端申请值（只收紧不放宽）。
func (b Budget) ClampByServer(max Budget) Budget {
	clamp := func(want, limit int) int {
		if want <= 0 || want > limit {
			return limit
		}
		return want
	}
	return Budget{
		MaxDurationSeconds: clamp(b.MaxDurationSeconds, max.MaxDurationSeconds),
		MaxModelCalls:      clamp(b.MaxModelCalls, max.MaxModelCalls),
		MaxToolCalls:       clamp(b.MaxToolCalls, max.MaxToolCalls),
		MaxIterations:      clamp(b.MaxIterations, max.MaxIterations),
		MaxOutputBytes:     clamp(b.MaxOutputBytes, max.MaxOutputBytes),
	}
}

// SessionInfo is the session metadata returned by the list endpoint.
type SessionInfo struct {
	ID        string `json:"session_id"`
	Title     string `json:"title"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// Run 是业务 Run 实体（与 Eino 内部消息解耦）。
type Run struct {
	ID              string    `json:"id"`
	SessionID       string    `json:"session_id"`
	OwnerID         string    `json:"owner_id"`
	Status          RunStatus `json:"status"`
	Mode            string    `json:"mode"`
	Goal            string    `json:"goal"`
	Constraints     []string  `json:"constraints,omitempty"`
	SuccessCriteria []string  `json:"success_criteria,omitempty"`
	Budget          Budget    `json:"budget"`
	CheckpointID    string    `json:"checkpoint_id,omitempty"`
	Attempt         int       `json:"attempt"`
	IdempotencyKey  string    `json:"idempotency_key,omitempty"`
	ErrorCode       string    `json:"error_code,omitempty"`
	ErrorSummary    string    `json:"error_summary,omitempty"`
	ResultJSON      string    `json:"result_json,omitempty"`
	CreatedAt       int64     `json:"created_at"`
	UpdatedAt       int64     `json:"updated_at"`
	StartedAt       int64     `json:"started_at,omitempty"`
	FinishedAt      int64     `json:"finished_at,omitempty"`
}

// Step 是 Plan-Execute 步骤实体。
type Step struct {
	ID            string     `json:"id"`
	RunID         string     `json:"run_id"`
	Index         int        `json:"step_index"`
	Description   string     `json:"description"`
	Status        StepStatus `json:"status"`
	Dependencies  []int      `json:"dependencies,omitempty"`
	ResultSummary string     `json:"result_summary,omitempty"`
	StartedAt     int64      `json:"started_at,omitempty"`
	FinishedAt    int64      `json:"finished_at,omitempty"`
}

// ApprovalStatus 是审批状态。
type ApprovalStatus string

const (
	ApprovalPending  ApprovalStatus = "pending"
	ApprovalApproved ApprovalStatus = "approved"
	ApprovalRejected ApprovalStatus = "rejected"
	ApprovalExpired  ApprovalStatus = "expired"
)

// Approval 是高风险工具调用的人工审批记录。
type Approval struct {
	ID          string         `json:"id"`
	RunID       string         `json:"run_id"`
	ToolCallID  string         `json:"tool_call_id"`
	ToolName    string         `json:"tool_name"`
	ArgsHash    string         `json:"args_hash"`
	RequestedBy string         `json:"requested_by"`
	ApprovedBy  string         `json:"approved_by,omitempty"`
	Status      ApprovalStatus `json:"status"`
	Reason      string         `json:"reason,omitempty"`
	ExpiresAt   int64          `json:"expires_at"`
	CreatedAt   int64          `json:"created_at"`
	DecidedAt   int64          `json:"decided_at,omitempty"`
}
