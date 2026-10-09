package runtime

import (
	"context"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/store"
)

func RecoverOrphans(ctx context.Context, tasks *store.TaskRepo, events *store.EventRepo) (int, error) {
	orphans, err := tasks.ListOrphanRuns(ctx, 100)
	if err != nil {
		return 0, err
	}
	fixed := 0
	for _, o := range orphans {
		switch o.Status {
		case domain.RunRunning:
			_ = tasks.UpdateStatus(ctx, o.OwnerID, o.ID, domain.RunFailed, "orphan_recovered", "process restarted while running; resume from last committed boundary")
			fixed++
		case domain.RunQueued, domain.RunPaused, domain.RunWaitingApproval:
			fixed++
		}
		_ = events
	}
	return fixed, nil
}
