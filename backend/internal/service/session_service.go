package service

import (
	"context"
	"strings"

	"rivus-agent-backend/internal/store"
)

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
