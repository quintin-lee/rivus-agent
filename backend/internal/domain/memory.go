package domain

// Memory 是一条会话级持久记忆（跨 run 共享，随会话删除级联清除）。
type Memory struct {
	ID          string `json:"id"`
	SessionID   string `json:"session_id"`
	OwnerID     string `json:"owner_id"`
	Content     string `json:"content"`
	SourceRunID string `json:"source_run_id,omitempty"`
	CreatedAt   int64  `json:"created_at"`
}
