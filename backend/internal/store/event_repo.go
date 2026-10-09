package store

import (
	"context"
	"database/sql"

	"github.com/google/uuid"
	"rivus-agent-backend/internal/domain"
)

// EventRepo 追加写入事件，SSE 通过 seq 支持 after 恢复读取。
type EventRepo struct{ db *sql.DB }

func NewEventRepo(db *sql.DB) *EventRepo { return &EventRepo{db: db} }

func newID(prefix string) string { return prefix + "_" + uuid.NewString() }

// Append 写入一条事件（只存摘要，不默认存完整原文）。
func (r *EventRepo) Append(ctx context.Context, runID string, typ domain.EventType, payloadJSON, sensitivity string) (int64, error) {
	if sensitivity == "" {
		sensitivity = "normal"
	}
	res, err := r.db.ExecContext(ctx, `INSERT INTO agent_events(run_id, event_type, payload_json, sensitivity, created_at)
		VALUES(?, ?, ?, ?, ?)`, runID, string(typ), payloadJSON, sensitivity, nowMs())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListAfter 按 seq 增序返回事件，支持 SSE 断连恢复。
func (r *EventRepo) ListAfter(ctx context.Context, runID string, after int64, limit int) ([]domain.AgentEvent, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT seq, event_type, payload_json, sensitivity, created_at
		FROM agent_events WHERE run_id = ? AND seq > ? ORDER BY seq LIMIT ?`, runID, after, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []domain.AgentEvent
	for rows.Next() {
		var e domain.AgentEvent
		var typ string
		e.RunID = runID
		if err := rows.Scan(&e.Seq, &typ, &e.PayloadJSON, &e.Sensitivity, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Type = domain.EventType(typ)
		out = append(out, e)
	}
	return out, rows.Err()
}
