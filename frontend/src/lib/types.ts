export type RunStatus =
  | 'queued'
  | 'running'
  | 'waiting_approval'
  | 'paused'
  | 'succeeded'
  | 'failed'
  | 'cancelled'
  | 'timed_out'

export type StepStatus =
  | 'pending'
  | 'running'
  | 'succeeded'
  | 'failed'
  | 'skipped'
  | 'awaiting_approval'

export type EventType =
  | 'run.created'
  | 'run.started'
  | 'run.finished'
  | 'run.failed'
  | 'run.cancelled'
  | 'run.resumed'
  | 'plan.created'
  | 'plan.updated'
  | 'step.started'
  | 'step.finished'
  | 'model.requested'
  | 'model.completed'
  | 'model.failed'
  | 'tool.requested'
  | 'tool.authorized'
  | 'tool.denied'
  | 'tool.started'
  | 'tool.completed'
  | 'tool.failed'
  | 'approval.requested'
  | 'approval.decided'
  | 'checkpoint.saved'
  | 'verification.passed'
  | 'verification.failed'

export type ApprovalStatus = 'pending' | 'approved' | 'rejected' | 'expired'

export interface AgentEvent {
  seq: number
  run_id: string
  event_type: EventType
  payload_json: string
  sensitivity: 'normal' | 'low' | 'medium' | 'high'
  created_at: number
}

export interface Budget {
  max_duration_seconds: number
  max_model_calls: number
  max_tool_calls: number
  max_iterations: number
  max_output_bytes: number
}

export interface Run {
  id: string
  session_id: string
  owner_id: string
  status: RunStatus
  mode: string
  goal: string
  constraints?: string[]
  success_criteria?: string[]
  budget: Budget
  checkpoint_id?: string
  attempt: number
  idempotency_key?: string
  error_code?: string
  error_summary?: string
  result_json?: string
  created_at: number
  updated_at: number
  started_at?: number
  finished_at?: number
}

export interface Step {
  id: string
  run_id: string
  step_index: number
  description: string
  status: StepStatus
  dependencies?: number[]
  result_summary?: string
  started_at?: number
  finished_at?: number
}

export interface Approval {
  id: string
  run_id: string
  tool_call_id: string
  tool_name: string
  args_hash: string
  requested_by: string
  approved_by?: string
  status: ApprovalStatus
  reason?: string
  expires_at: number
  created_at: number
  decided_at?: number
}

export interface Session {
  session_id: string
  title: string
  created_at: number
  updated_at: number
}

export interface CreateRunReq {
  session_id: string
  goal: string
  constraints?: string[]
  success_criteria?: string[]
  mode?: 'react' | 'plan_execute'
  budget?: Partial<Budget>
}

export interface SessionListResponse {
  sessions: Session[]
}

export interface RunListResponse {
  runs: Run[]
}

export interface ModelSettings {
  provider: string
  base_url: string
  model: string
  api_key_set: boolean
  updated_at?: number
}
