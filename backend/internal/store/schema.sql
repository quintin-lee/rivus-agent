CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  applied_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
  id TEXT PRIMARY KEY,
  owner_id TEXT NOT NULL,
  title TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_owner ON sessions(owner_id, created_at);

CREATE TABLE IF NOT EXISTS runs (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL,
  owner_id TEXT NOT NULL,
  status TEXT NOT NULL,
  mode TEXT NOT NULL,
  goal TEXT NOT NULL,
  constraints_json TEXT,
  success_criteria_json TEXT,
  budget_json TEXT NOT NULL,
  checkpoint_id TEXT,
  attempt INTEGER NOT NULL DEFAULT 1,
  idempotency_key TEXT,
  started_at INTEGER,
  finished_at INTEGER,
  error_code TEXT,
  error_summary TEXT,
  result_json TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  FOREIGN KEY(session_id) REFERENCES sessions(id)
);
CREATE INDEX IF NOT EXISTS idx_runs_owner ON runs(owner_id, created_at);
CREATE INDEX IF NOT EXISTS idx_runs_status ON runs(status, created_at);
CREATE INDEX IF NOT EXISTS idx_runs_session ON runs(session_id, created_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_runs_owner_idem ON runs(owner_id, idempotency_key);

CREATE TABLE IF NOT EXISTS run_steps (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL,
  step_index INTEGER NOT NULL,
  description TEXT NOT NULL,
  status TEXT NOT NULL,
  dependencies_json TEXT,
  result_summary TEXT,
  verification_json TEXT,
  started_at INTEGER,
  finished_at INTEGER,
  FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_steps_run ON run_steps(run_id, step_index);

CREATE TABLE IF NOT EXISTS agent_events (
  seq INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id TEXT NOT NULL,
  event_type TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  sensitivity TEXT NOT NULL DEFAULT 'normal',
  created_at INTEGER NOT NULL,
  FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_events_run ON agent_events(run_id, seq);

CREATE TABLE IF NOT EXISTS approvals (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL,
  tool_call_id TEXT NOT NULL,
  tool_name TEXT NOT NULL,
  args_hash TEXT NOT NULL,
  requested_by TEXT NOT NULL,
  approved_by TEXT,
  status TEXT NOT NULL,
  reason TEXT,
  expires_at INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  decided_at INTEGER,
  FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_approvals_run ON approvals(run_id, created_at);

CREATE TABLE IF NOT EXISTS agent_checkpoints (
  checkpoint_id TEXT PRIMARY KEY,
  payload BLOB NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS server_settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);
