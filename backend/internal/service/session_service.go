package service

import (
	"context"
	"errors"
	"strings"

	"rivus-agent-backend/internal/store"
)

// ErrEmptyTitle 表示重命名请求的标题为空（重命名为空无意义，与创建时的默认值不同）。
var ErrEmptyTitle = errors.New("title is empty")

type SessionService struct {
	tasks *store.TaskRepo
}

func NewSessionService(tasks *store.TaskRepo) *SessionService {
	return &SessionService{tasks: tasks}
}

func (s *SessionService) Create(ctx context.Context, ownerID, title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "untitled"
	}
	if len(title) > 200 {
		title = title[:200]
	}
	return s.tasks.CreateSession(ctx, ownerID, title)
}

// Rename 重命名会话：TrimSpace 后空标题拒绝，超长截断（与 Create 一致）。
func (s *SessionService) Rename(ctx context.Context, ownerID, sessionID, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return ErrEmptyTitle
	}
	if len(title) > 200 {
		title = title[:200]
	}
	return s.tasks.RenameSession(ctx, ownerID, sessionID, title)
}
