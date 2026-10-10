package store

import (
	"context"
	"database/sql"

	"rivus-agent-backend/internal/domain"
)

// ApprovalRepo 管理人工审批记录。
type ApprovalRepo struct{ db *sql.DB }

func NewApprovalRepo(db *sql.DB) *ApprovalRepo { return &ApprovalRepo{db: db} }

func (r *ApprovalRepo) Create(ctx context.Context, a *domain.Approval) error {
	a.ID = newID("apr")
	a.CreatedAt = nowMs()
	a.Status = domain.ApprovalPending
	_, err := r.db.ExecContext(ctx, `INSERT INTO approvals(id, run_id, tool_call_id, tool_name, args_hash, requested_by, status, reason, expires_at, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.RunID, a.ToolCallID, a.ToolName, a.ArgsHash, a.RequestedBy, string(a.Status), a.Reason, a.ExpiresAt, a.CreatedAt)
	return err
}

func (r *ApprovalRepo) Get(ctx context.Context, id string) (*domain.Approval, error) {
	var a domain.Approval
	var status string
	var approvedBy, reason sql.NullString
	var decidedAt sql.NullInt64
	err := r.db.QueryRowContext(ctx, `SELECT id, run_id, tool_call_id, tool_name, args_hash, requested_by, approved_by, status, reason, expires_at, created_at, decided_at
		FROM approvals WHERE id = ?`, id).Scan(
		&a.ID, &a.RunID, &a.ToolCallID, &a.ToolName, &a.ArgsHash, &a.RequestedBy, &approvedBy, &status, &reason,
		&a.ExpiresAt, &a.CreatedAt, &decidedAt)
	if err != nil {
		return nil, err
	}
	a.Status = domain.ApprovalStatus(status)
	a.ApprovedBy, a.Reason = strOr(approvedBy, ""), strOr(reason, "")
	a.DecidedAt = intOr(decidedAt)
	return &a, nil
}

// Decide 审批终态只能写一次；过期由调用方判定为 expired。
func (r *ApprovalRepo) Decide(ctx context.Context, id string, approved bool, by, reason string) error {
	status := domain.ApprovalRejected
	if approved {
		status = domain.ApprovalApproved
	}
	res, err := r.db.ExecContext(ctx, `UPDATE approvals SET status = ?, approved_by = ?, reason = ?, decided_at = ?
		WHERE id = ? AND status = ?`, string(status), by, reason, nowMs(), id, string(domain.ApprovalPending))
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return domain.ErrConflict
	}
	return nil
}

// ListByRun 返回某 Run 的全部审批记录（恢复时收集已批清单用）。
func (r *ApprovalRepo) ListByRun(ctx context.Context, runID string) ([]domain.Approval, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, run_id, tool_call_id, tool_name, args_hash, requested_by, approved_by, status, reason, expires_at, created_at, decided_at
		FROM approvals WHERE run_id = ? ORDER BY created_at`, runID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []domain.Approval{}
	for rows.Next() {
		var a domain.Approval
		var status string
		var approvedBy, reason sql.NullString
		var decidedAt sql.NullInt64
		if err := rows.Scan(&a.ID, &a.RunID, &a.ToolCallID, &a.ToolName, &a.ArgsHash, &a.RequestedBy,
			&approvedBy, &status, &reason, &a.ExpiresAt, &a.CreatedAt, &decidedAt); err != nil {
			return nil, err
		}
		a.Status = domain.ApprovalStatus(status)
		a.ApprovedBy, a.Reason = strOr(approvedBy, ""), strOr(reason, "")
		a.DecidedAt = intOr(decidedAt)
		out = append(out, a)
	}
	return out, rows.Err()
}

// FindPendingByToolCall 查找同一 tool_call 的待审批记录（幂等：重复中断不重复建单）。
func (r *ApprovalRepo) FindPendingByToolCall(ctx context.Context, runID, toolCallID string) (*domain.Approval, error) {
	var id string
	err := r.db.QueryRowContext(ctx, `SELECT id FROM approvals WHERE run_id = ? AND tool_call_id = ? AND status = ? ORDER BY created_at DESC LIMIT 1`,
		runID, toolCallID, string(domain.ApprovalPending)).Scan(&id)
	if err != nil {
		return nil, err
	}
	return r.Get(ctx, id)
}
