package runtime

import (
	"sync"
	"time"

	"rivus-agent-backend/internal/domain"
)

type Usage struct {
	ModelCalls  int
	ToolCalls   int
	Iterations  int
	OutputBytes int
	StartedAt   time.Time
}

type BudgetTracker struct {
	mu    sync.Mutex
	limit domain.Budget
	usage Usage
}

func NewBudgetTracker(limit domain.Budget) *BudgetTracker {
	return &BudgetTracker{limit: limit, usage: Usage{StartedAt: time.Now()}}
}

func (b *BudgetTracker) CheckIteration() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.usage.Iterations >= b.limit.MaxIterations {
		return domain.ErrBudgetExceeded
	}
	if b.usage.ModelCalls >= b.limit.MaxModelCalls {
		return domain.ErrBudgetExceeded
	}
	if time.Since(b.usage.StartedAt) > time.Duration(b.limit.MaxDurationSeconds)*time.Second {
		return domain.ErrTimeout
	}
	return nil
}

func (b *BudgetTracker) CheckTool() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.usage.ToolCalls >= b.limit.MaxToolCalls {
		return domain.ErrBudgetExceeded
	}
	return nil
}

func (b *BudgetTracker) AddModelCall()   { b.mu.Lock(); b.usage.ModelCalls++; b.mu.Unlock() }
func (b *BudgetTracker) AddToolCall()    { b.mu.Lock(); b.usage.ToolCalls++; b.mu.Unlock() }
func (b *BudgetTracker) AddIteration()   { b.mu.Lock(); b.usage.Iterations++; b.mu.Unlock() }
func (b *BudgetTracker) AddOutput(n int) { b.mu.Lock(); b.usage.OutputBytes += n; b.mu.Unlock() }
func (b *BudgetTracker) Snapshot() Usage {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.usage
}
