package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"rivus-agent-backend/internal/domain"
)

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

// RenameSession 更新会话标题（属主校验沿用 GetSession，updated_at 推进到 now）。
func (r *TaskRepo) RenameSession(ctx context.Context, ownerID, sessionID, title string) error {
	if _, _, _, _, err := r.GetSession(ctx, ownerID, sessionID); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE sessions SET title = ?, updated_at = ? WHERE id = ? AND owner_id = ?`,
		title, nowMs(), sessionID, ownerID)
	return err
}

// EncodeCursor 将排序键与行 ID 编码为不透明游标（base64url of "sortValue:id"）。

func (r *TaskRepo) ListSessions(ctx context.Context, ownerID, q string, limit, offset int, cursor string) ([]domain.SessionInfo, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	query := `SELECT id, title, created_at, updated_at FROM sessions WHERE owner_id = ?`
	args := []any{ownerID}
	if term := strings.ToLower(strings.TrimSpace(q)); term != "" {
		query += ` AND LOWER(title) LIKE '%' || ? || '%'`
		args = append(args, term)
	}
	if cursor != "" {
		sortVal, lastID, err := DecodeCursor(cursor)
		if err != nil {
			return nil, fmt.Errorf("invalid cursor: %w", err)
		}
		offset = 0
		query += ` AND (updated_at < ? OR (updated_at = ? AND id < ?))`
		args = append(args, sortVal, sortVal, lastID)
	}
	query += ` ORDER BY updated_at DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := r.db.QueryContext(ctx, query, args...)
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

// runsFTSQuery 生成 runs 全文过滤片段：≥3 字节词条走 FTS5 trigram 短语匹配（双引号转义），
// 更短词条返回 ok=false 由调用方走 LIKE 兜底。两路返回同一行集。
