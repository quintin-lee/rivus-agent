package store

import (
	"context"
	"database/sql"

	"rivus-agent-backend/internal/domain"
)

// MemoryRepo 负责 session_memories 持久化（LRU 100 条/会话，会话删除级联）。
type MemoryRepo struct{ db *sql.DB }

func NewMemoryRepo(db *sql.DB) *MemoryRepo { return &MemoryRepo{db: db} }

const memoryMaxPerSession = 100

// Write 插入一条记忆并裁剪到最近 100 条（超出删最旧）。
func (r *MemoryRepo) Write(ctx context.Context, m *domain.Memory) error {
	if m.ID == "" {
		m.ID = newID("mem")
	}
	if m.CreatedAt == 0 {
		m.CreatedAt = nowMs()
	}
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO session_memories(id, session_id, owner_id, content, source_run_id, created_at) VALUES(?, ?, ?, ?, ?, ?)`,
		m.ID, m.SessionID, m.OwnerID, m.Content, m.SourceRunID, m.CreatedAt); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM session_memories WHERE session_id = ? AND rowid NOT IN
		 (SELECT rowid FROM session_memories WHERE session_id = ? ORDER BY created_at DESC, rowid DESC LIMIT ?)`,
		m.SessionID, m.SessionID, memoryMaxPerSession)
	return err
}

// Latest 返回某会话最近 limit 条记忆（新→旧；同毫秒靠 rowid 兜底排序）。
func (r *MemoryRepo) Latest(ctx context.Context, sessionID string, limit int) ([]domain.Memory, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, session_id, owner_id, content, source_run_id, created_at
		 FROM session_memories WHERE session_id = ? ORDER BY created_at DESC, rowid DESC LIMIT ?`,
		sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []domain.Memory
	for rows.Next() {
		var m domain.Memory
		var src sql.NullString
		if err := rows.Scan(&m.ID, &m.SessionID, &m.OwnerID, &m.Content, &src, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.SourceRunID = src.String
		out = append(out, m)
	}
	return out, rows.Err()
}
