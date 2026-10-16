package runtime

import (
	"context"

	"rivus-agent-backend/internal/domain"
)

// TaskContext 携带父 run 身份/预算/深度，注入 ctx 让 task 工具能读。
type TaskContext struct {
	OwnerID, ParentRunID, SessionID string
	ParentBudget                    domain.Budget
	Depth                           int // 0=顶层(可 spawn 子)；>=1=子(禁 task)
}

type taskCtxKey struct{}

// WithTaskContext 把 TaskContext 绑到 ctx。
func WithTaskContext(ctx context.Context, tc TaskContext) context.Context {
	return context.WithValue(ctx, taskCtxKey{}, tc)
}

// TaskContextFrom 读回 ctx 里的 TaskContext；未注入时 ok=false。
func TaskContextFrom(ctx context.Context) (TaskContext, bool) {
	tc, ok := ctx.Value(taskCtxKey{}).(TaskContext)
	return tc, ok
}
