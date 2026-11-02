package evals

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/tests/testutil"
)

// 场景 41：保留策略实删——cutoff=now+5000 使刚完成的终态 run 可被 PruneRuns 清理；
// 同一 session 的 running run 因 status 非终态而存活。
func TestScenarioPruneRemovesOldTerminal(t *testing.T) {
	Run(t, Scenario{
		Name: "prune_removes_old_terminal", Category: "agent",
		Goal: "prune removes terminal runs older than cutoff but keeps running runs",
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueText("好的，已完成。")
			return ""
		},
		Want: Expect{TerminalStatus: domain.RunSucceeded, MaxModelCalls: 1},
		PostCheck: func(t *testing.T, h *testutil.Harness, _ string, run domain.Run) {
			ctx := context.Background()

			// 主 run 是终态；造一个 running run（同 session，同 owner）用于"存活"对照
			ownerID := run.OwnerID
			var sesID string
			if err := h.DB.QueryRow(`SELECT session_id FROM runs WHERE id = ?`, run.ID).Scan(&sesID); err != nil {
				t.Fatalf("read session_id: %v", err)
			}
			now := time.Now().UnixMilli()
			_, err := h.DB.Exec(`INSERT INTO runs(id, session_id, owner_id, status, mode, goal, budget_json, attempt, created_at, updated_at)
				VALUES (?,?,?,?,?,?,?,1,?,?)`,
				"r29-running-"+run.ID, sesID, ownerID, "running", "react", "keep me",
				`{"max_duration_seconds":60}`, now, now)
			if err != nil {
				t.Fatalf("seed running run: %v", err)
			}

			cutoff := now + 5000 // 使刚完成的终态 run 满足 updated_at < cutoff

			n, err := h.Tasks.PruneRuns(ctx, cutoff)
			if err != nil {
				t.Fatalf("prune: %v", err)
			}
			if n < 1 {
				t.Fatalf("prune removed %d, want >=1 (main terminal run)", n)
			}

			// 主 run 被删：GetRun 返回 sql.ErrNoRows
			if _, err := h.Tasks.GetRun(ctx, ownerID, run.ID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("main run still present after prune: %v", err)
			}
			// running run 存活
			if _, err := h.Tasks.GetRun(ctx, ownerID, "r29-running-"+run.ID); err != nil {
				t.Fatalf("running run pruned, want survives: %v", err)
			}
		},
	})
}

// 场景 42：dry-run——CountPrunable 报出可清理数但从不删除，PruneRuns 前后计数一致。
func TestScenarioPruneDryRun(t *testing.T) {
	Run(t, Scenario{
		Name: "prune_dry_run_reports_only", Category: "agent",
		Goal: "dry-run reports prunable count without deleting anything",
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueText("好的，已完成。")
			return ""
		},
		Want: Expect{TerminalStatus: domain.RunSucceeded, MaxModelCalls: 1},
		PostCheck: func(t *testing.T, h *testutil.Harness, _ string, run domain.Run) {
			ctx := context.Background()
			cutoff := time.Now().UnixMilli() + 5000

			// dry-run：只 CountPrunable，绝不调用 PruneRuns
			prunable, err := h.Tasks.CountPrunable(ctx, cutoff)
			if err != nil {
				t.Fatalf("count prunable: %v", err)
			}
			if prunable < 1 {
				t.Fatalf("count prunable = %d, want >=1 (main terminal run)", prunable)
			}

			// 不调用 PruneRuns：主 run 仍在
			if _, err := h.Tasks.GetRun(ctx, run.OwnerID, run.ID); err != nil {
				t.Fatalf("dry-run must not delete, but main run missing: %v", err)
			}

			// 复数 CountPrunable 稳定（无副作用，前后一致）
			again, _ := h.Tasks.CountPrunable(ctx, cutoff)
			if again != prunable {
				t.Fatalf("count prunable changed: %d -> %d (must be read-only)", prunable, again)
			}
		},
	})
}
