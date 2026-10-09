package domain

// EventType 是业务事件类型（稳定的业务协议，不直接暴露 Eino 内部事件）。
type EventType string

const (
	EvtRunCreated        EventType = "run.created"
	EvtRunStarted        EventType = "run.started"
	EvtRunFinished       EventType = "run.finished"
	EvtRunFailed         EventType = "run.failed"
	EvtRunCancelled      EventType = "run.cancelled"
	EvtPlanCreated       EventType = "plan.created"
	EvtPlanUpdated       EventType = "plan.updated"
	EvtStepStarted       EventType = "step.started"
	EvtStepFinished      EventType = "step.finished"
	EvtModelRequested    EventType = "model.requested"
	EvtModelCompleted    EventType = "model.completed"
	EvtModelFailed       EventType = "model.failed"
	EvtToolRequested     EventType = "tool.requested"
	EvtToolAuthorized    EventType = "tool.authorized"
	EvtToolDenied        EventType = "tool.denied"
	EvtToolStarted       EventType = "tool.started"
	EvtToolCompleted     EventType = "tool.completed"
	EvtToolFailed        EventType = "tool.failed"
	EvtApprovalRequested EventType = "approval.requested"
	EvtApprovalDecided   EventType = "approval.decided"
	EvtCheckpointSaved   EventType = "checkpoint.saved"
	EvtRunResumed        EventType = "run.resumed"
	EvtVerifyPassed      EventType = "verification.passed"
	EvtVerifyFailed      EventType = "verification.failed"
)

// AgentEvent 是追加写入 agent_events 的记录。
type AgentEvent struct {
	Seq         int64     `json:"seq"`
	RunID       string    `json:"run_id"`
	Type        EventType `json:"event_type"`
	PayloadJSON string    `json:"payload_json"`
	Sensitivity string    `json:"sensitivity"`
	CreatedAt   int64     `json:"created_at"`
}
