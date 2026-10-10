package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"rivus-agent-backend/internal/domain"
)

// TaskRepo 负责 sessions / runs / run_steps 持久化。写事务保持短小，绝不在事务内等待模型或外部工具。
type TaskRepo struct{ db *sql.DB }

func NewTaskRepo(db *sql.DB) *TaskRepo { return &TaskRepo{db: db} }

func nowMs() int64 { return time.Now().UnixMilli() }

func (r *TaskRepo) CreateSession(ctx context.Context, ownerID, title string) (string, error) {
	id := newID("ses")
	ts := nowMs()
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO sessions(id, owner_id, title, created_at, updated_at) VALUES(?, ?, ?, ?, ?)`,
		id, ownerID, title, ts, ts)
	return id, err
}

func (r *TaskRepo) GetSession(ctx context.Context, ownerID, sessionID string) (string, string, int64, int64, error) {
	var owner, title string
	var ca, ua int64
	err := r.db.QueryRowContext(ctx,
		`SELECT owner_id, title, created_at, updated_at FROM sessions WHERE id = ?`, sessionID).Scan(&owner, &title, &ca, &ua)
	if err != nil {
		return "", "", 0, 0, err
	}
	if owner != ownerID {
		return "", "", 0, 0, domain.ErrForbidden
	}
	return owner, title, ca, ua, nil
}

func (r *TaskRepo) DeleteSession(ctx context.Context, ownerID, sessionID string) error {
	if _, _, _, _, err := r.GetSession(ctx, ownerID, sessionID); err != nil {
		return err
	}
	var active int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE session_id = ?
		AND status IN ('queued','running','waiting_approval','paused')`, sessionID).Scan(&active); err != nil {
		return err
	}
	if active > 0 {
		return domain.ErrConflict
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id FROM runs WHERE session_id = ?`, sessionID)
	if err != nil {
		return err
	}
	var runIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		runIDs = append(runIDs, id)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range []string{
		`DELETE FROM agent_events WHERE run_id IN (SELECT id FROM runs WHERE session_id = ?)`,
		`DELETE FROM approvals WHERE run_id IN (SELECT id FROM runs WHERE session_id = ?)`,
		`DELETE FROM run_steps WHERE run_id IN (SELECT id FROM runs WHERE session_id = ?)`,
		`DELETE FROM runs WHERE session_id = ?`,
		`DELETE FROM sessions WHERE id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, sessionID); err != nil {
			return err
		}
	}
	for _, rid := range runIDs {
		if _, err := tx.ExecContext(ctx, `DELETE FROM agent_checkpoints WHERE checkpoint_id LIKE 'ckpt_' || ? || '%'`, rid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *TaskRepo) ListSessions(ctx context.Context, ownerID string) ([]domain.SessionInfo, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, title, created_at, updated_at FROM sessions WHERE owner_id = ? ORDER BY updated_at DESC`,
		ownerID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []domain.SessionInfo{}
	for rows.Next() {
		var s domain.SessionInfo
		var title sql.NullString
		if err := rows.Scan(&s.ID, &title, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		s.Title = strOr(title, "")
		out = append(out, s)
	}
	return out, rows.Err()
}

// CreateRun 插入 Run；相同 (owner_id, idempotency_key) 返回已存在 Run（幂等）。
func (r *TaskRepo) CreateRun(ctx context.Context, run *domain.Run) error {
	cj, _ := json.Marshal(run.Constraints)
	scj, _ := json.Marshal(run.SuccessCriteria)
	bj, _ := json.Marshal(run.Budget)
	ts := nowMs()
	run.CreatedAt, run.UpdatedAt = ts, ts
	_, err := r.db.ExecContext(ctx, `INSERT INTO runs(id, session_id, owner_id, status, mode, goal,
		constraints_json, success_criteria_json, budget_json, checkpoint_id, attempt, idempotency_key,
		started_at, finished_at, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.SessionID, run.OwnerID, string(run.Status), run.Mode, run.Goal,
		string(cj), string(scj), string(bj), run.CheckpointID, run.Attempt, nullIfEmpty(run.IdempotencyKey),
		nullInt(run.StartedAt), nullInt(run.FinishedAt), run.CreatedAt, run.UpdatedAt)
	return err
}

// FindByIdempotencyKey 命中幂等键时返回已存在 Run。
func (r *TaskRepo) FindByIdempotencyKey(ctx context.Context, ownerID, key string) (*domain.Run, error) {
	if key == "" {
		return nil, sql.ErrNoRows
	}
	row := r.db.QueryRowContext(ctx, `SELECT id FROM runs WHERE owner_id = ? AND idempotency_key = ?`, ownerID, key)
	var id string
	if err := row.Scan(&id); err != nil {
		return nil, err
	}
	return r.GetRun(ctx, ownerID, id)
}

func (r *TaskRepo) GetRun(ctx context.Context, ownerID, runID string) (*domain.Run, error) {
	var run domain.Run
	var status, mode, goal string
	var cj, scj, bj sql.NullString
	var cpid, idem, ecode, esum, resj sql.NullString
	var sa, fa sql.NullInt64
	err := r.db.QueryRowContext(ctx, `SELECT id, session_id, owner_id, status, mode, goal,
		constraints_json, success_criteria_json, budget_json, checkpoint_id, attempt, idempotency_key,
		started_at, finished_at, error_code, error_summary, result_json, created_at, updated_at
		FROM runs WHERE id = ?`, runID).Scan(
		&run.ID, &run.SessionID, &run.OwnerID, &status, &mode, &goal,
		&cj, &scj, &bj, &cpid, &run.Attempt, &idem, &sa, &fa, &ecode, &esum, &resj,
		&run.CreatedAt, &run.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if run.OwnerID != ownerID {
		return nil, domain.ErrForbidden
	}
	run.Status, run.Mode, run.Goal = domain.RunStatus(status), mode, goal
	_ = json.Unmarshal([]byte(strOr(cj, "[]")), &run.Constraints)
	_ = json.Unmarshal([]byte(strOr(scj, "[]")), &run.SuccessCriteria)
	_ = json.Unmarshal([]byte(strOr(bj, "{}")), &run.Budget)
	run.CheckpointID, run.IdempotencyKey = strOr(cpid, ""), strOr(idem, "")
	run.ErrorCode, run.ErrorSummary, run.ResultJSON = strOr(ecode, ""), strOr(esum, ""), strOr(resj, "")
	run.StartedAt, run.FinishedAt = intOr(sa), intOr(fa)
	return &run, nil
}

func (r *TaskRepo) ListRunsBySession(ctx context.Context, ownerID, sessionID string, limit int) ([]domain.Run, error) {
	if _, _, _, _, err := r.GetSession(ctx, ownerID, sessionID); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, session_id, owner_id, status, mode, goal,
		        constraints_json, success_criteria_json, budget_json,
		        checkpoint_id, attempt, idempotency_key, started_at, finished_at,
		        error_code, error_summary, result_json, created_at, updated_at
	         FROM runs WHERE session_id = ? AND owner_id = ? ORDER BY created_at DESC LIMIT ?`,
		sessionID, ownerID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []domain.Run{}
	for rows.Next() {
		var run domain.Run
		var status, mode, goal string
		var cj, scj, bj sql.NullString
		var cpid, idem, ecode, esum, resj sql.NullString
		var sa, fa sql.NullInt64
		if err := rows.Scan(
			&run.ID, &run.SessionID, &run.OwnerID, &status, &mode, &goal,
			&cj, &scj, &bj, &cpid, &run.Attempt, &idem, &sa, &fa, &ecode, &esum, &resj,
			&run.CreatedAt, &run.UpdatedAt,
		); err != nil {
			return nil, err
		}
		run.Status = domain.RunStatus(status)
		run.Mode = mode
		run.Goal = goal
		_ = json.Unmarshal([]byte(strOr(cj, "[]")), &run.Constraints)
		_ = json.Unmarshal([]byte(strOr(scj, "[]")), &run.SuccessCriteria)
		_ = json.Unmarshal([]byte(strOr(bj, "{}")), &run.Budget)
		run.CheckpointID = strOr(cpid, "")
		run.IdempotencyKey = strOr(idem, "")
		run.ErrorCode = strOr(ecode, "")
		run.ErrorSummary = strOr(esum, "")
		run.ResultJSON = strOr(resj, "")
		run.StartedAt = intOr(sa)
		run.FinishedAt = intOr(fa)
		out = append(out, run)
	}
	return out, rows.Err()
}

// UpdateStatus 做状态机校验后更新状态。
func (r *TaskRepo) UpdateStatus(ctx context.Context, ownerID, runID string, to domain.RunStatus, errCode, errSummary string) error {
	run, err := r.GetRun(ctx, ownerID, runID)
	if err != nil {
		return err
	}
	if !domain.ValidTransition(run.Status, to) {
		return domain.ErrConflict
	}
	ts := nowMs()
	var finished any
	if to.Terminal() {
		finished = ts
	}
	_, err = r.db.ExecContext(ctx,
		`UPDATE runs SET status = ?, error_code = ?, error_summary = ?, finished_at = COALESCE(?, finished_at), updated_at = ? WHERE id = ?`,
		string(to), errCode, errSummary, finished, ts, runID)
	return err
}

func (r *TaskRepo) RetryFailed(ctx context.Context, ownerID, runID string) error {
	if _, err := r.GetRun(ctx, ownerID, runID); err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx, `UPDATE runs SET status = ?, attempt = attempt + 1,
		error_code = '', error_summary = '', updated_at = ? WHERE id = ? AND status = 'failed'`,
		string(domain.RunRunning), nowMs(), runID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return domain.ErrConflict
	}
	return nil
}

// SetCheckpointID 关联 Eino checkpoint。
func (r *TaskRepo) SetCheckpointID(ctx context.Context, runID, cpid string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE runs SET checkpoint_id = ?, updated_at = ? WHERE id = ?`, cpid, nowMs(), runID)
	return err
}

// SetResult 保存结构化结果与证据。
func (r *TaskRepo) SetResult(ctx context.Context, runID, resultJSON string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE runs SET result_json = ?, updated_at = ? WHERE id = ?`, resultJSON, nowMs(), runID)
	return err
}

// MarkStarted 记录启动时间并进入 running。
func (r *TaskRepo) MarkStarted(ctx context.Context, ownerID, runID string) error {
	run, err := r.GetRun(ctx, ownerID, runID)
	if err != nil {
		return err
	}
	if run.Status != domain.RunQueued && run.Status != domain.RunPaused {
		return domain.ErrConflict
	}
	ts := nowMs()
	_, err = r.db.ExecContext(ctx,
		`UPDATE runs SET status = ?, started_at = COALESCE(started_at, ?), updated_at = ? WHERE id = ?`,
		string(domain.RunRunning), ts, ts, runID)
	return err
}

// CreateSteps 批量写入 Plan-Execute 步骤。
func (r *TaskRepo) CreateSteps(ctx context.Context, runID string, steps []domain.Step) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, s := range steps {
		dep, _ := json.Marshal(s.Dependencies)
		_, err := tx.ExecContext(ctx, `INSERT INTO run_steps(id, run_id, step_index, description, status, dependencies_json, started_at, finished_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
			s.ID, runID, s.Index, s.Description, string(s.Status), string(dep), nullInt(s.StartedAt), nullInt(s.FinishedAt))
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListSteps 按序号返回步骤。
func (r *TaskRepo) ListSteps(ctx context.Context, ownerID, runID string) ([]domain.Step, error) {
	if _, err := r.GetRun(ctx, ownerID, runID); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id, step_index, description, status, dependencies_json, result_summary, started_at, finished_at
		FROM run_steps WHERE run_id = ? ORDER BY step_index`, runID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []domain.Step{}
	for rows.Next() {
		var s domain.Step
		var status string
		var dep sql.NullString
		var summary sql.NullString
		var sa, fa sql.NullInt64
		if err := rows.Scan(&s.ID, &s.Index, &s.Description, &status, &dep, &summary, &sa, &fa); err != nil {
			return nil, err
		}
		s.RunID, s.Status = runID, domain.StepStatus(status)
		_ = json.Unmarshal([]byte(strOr(dep, "[]")), &s.Dependencies)
		s.ResultSummary = strOr(summary, "")
		s.StartedAt, s.FinishedAt = intOr(sa), intOr(fa)
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListOrphanRuns 返回启动恢复时需检查的非终态 Run（bounded）。
func (r *TaskRepo) ListOrphanRuns(ctx context.Context, limit int) ([]domain.Run, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, session_id, owner_id, status, mode, goal, budget_json, checkpoint_id, attempt, created_at, updated_at
		FROM runs WHERE status IN ('queued','running','waiting_approval','paused') ORDER BY created_at LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []domain.Run{}
	for rows.Next() {
		var run domain.Run
		var status, bj string
		var cpid sql.NullString
		if err := rows.Scan(&run.ID, &run.SessionID, &run.OwnerID, &status, &run.Mode, &run.Goal, &bj, &cpid, &run.Attempt, &run.CreatedAt, &run.UpdatedAt); err != nil {
			return nil, err
		}
		run.Status = domain.RunStatus(status)
		_ = json.Unmarshal([]byte(bj), &run.Budget)
		run.CheckpointID = strOr(cpid, "")
		out = append(out, run)
	}
	return out, rows.Err()
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func strOr(n sql.NullString, def string) string {
	if n.Valid {
		return n.String
	}
	return def
}

func intOr(n sql.NullInt64) int64 {
	if n.Valid {
		return n.Int64
	}
	return 0
}
