package store

import (
	"context"
	"database/sql"
)

// CheckpointStore 实现 Eino CheckPointStore 的 Set/Get（参数化 SQL + UPSERT，错误直接返回，绝不静默退化）。
type CheckpointStore struct{ db *sql.DB }

func NewCheckpointStore(db *sql.DB) *CheckpointStore { return &CheckpointStore{db: db} }

func (s *CheckpointStore) Set(ctx context.Context, checkpointID string, checkpoint []byte) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent_checkpoints(checkpoint_id, payload, updated_at)
		VALUES(?, ?, ?) ON CONFLICT(checkpoint_id) DO UPDATE SET payload = excluded.payload, updated_at = excluded.updated_at`,
		checkpointID, checkpoint, nowMs())
	return err
}

func (s *CheckpointStore) Get(ctx context.Context, checkpointID string) ([]byte, bool, error) {
	var payload []byte
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM agent_checkpoints WHERE checkpoint_id = ?`, checkpointID).Scan(&payload)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return payload, true, nil
}

// Delete 清理终态 Run 的 checkpoint（可选实现，供 Eino CheckPointDeleter 语义使用）。
func (s *CheckpointStore) Delete(ctx context.Context, checkpointID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM agent_checkpoints WHERE checkpoint_id = ?`, checkpointID)
	return err
}
