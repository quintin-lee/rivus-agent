package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"rivus-agent-backend/internal/domain"
)

func ftsRun(t *testing.T, tasks *TaskRepo, ses, id, goal string) {
	t.Helper()
	if err := tasks.CreateRun(context.Background(), &domain.Run{
		ID: id, SessionID: ses, OwnerID: "u1", Status: domain.RunSucceeded,
		Mode: "react", Goal: goal,
		Budget:  domain.Budget{MaxDurationSeconds: 60, MaxModelCalls: 5, MaxToolCalls: 5, MaxIterations: 3, MaxOutputBytes: 1000},
		Attempt: 1,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestFTSSyncOnWrite(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	tasks := NewTaskRepo(db)
	ses, err := tasks.CreateSession(ctx, "u1", "fts-ses")
	if err != nil {
		t.Fatal(err)
	}
	ftsRun(t, tasks, ses, "run_a", "alpha task")

	// goal 立即可搜
	rows, err := db.Query(`SELECT run_id FROM runs_fts WHERE runs_fts MATCH ?`, `"alpha"`)
	if err != nil {
		t.Fatalf("fts match: %v", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) != 1 || ids[0] != "run_a" {
		t.Fatalf("goal match = %v, want [run_a]", ids)
	}

	// SetResult 后 result 可搜
	if err := tasks.SetResult(ctx, "run_a", `{"summary":"RESULTMARKER 报告"}`); err != nil {
		t.Fatal(err)
	}
	rows, err = db.Query(`SELECT run_id FROM runs_fts WHERE runs_fts MATCH ?`, `"resultmarker"`)
	if err != nil {
		t.Fatalf("fts match result: %v", err)
	}
	ids = nil
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) != 1 || ids[0] != "run_a" {
		t.Fatalf("result match = %v, want [run_a]", ids)
	}

	// 覆盖 result 后旧内容不再命中
	if err := tasks.SetResult(ctx, "run_a", `{"summary":"其他内容"}`); err != nil {
		t.Fatal(err)
	}
	rows, err = db.Query(`SELECT run_id FROM runs_fts WHERE runs_fts MATCH ?`, `"resultmarker"`)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for rows.Next() {
		count++
	}
	rows.Close()
	if count != 0 {
		t.Fatalf("stale result still matched, got %d rows", count)
	}
}

func TestFTSBackfillOnMigrate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fts-backfill.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tasks := NewTaskRepo(db)
	ses, err := tasks.CreateSession(ctx, "u1", "fts-ses")
	if err != nil {
		t.Fatal(err)
	}
	ftsRun(t, tasks, ses, "run_b", "backfill target xyz")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// 模拟旧版本库：FTS 表 + 迁移记录被移除（老代码创建的库没有 runs_fts）
	db2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db2.Exec(`DROP TABLE runs_fts`); err != nil {
		t.Fatal(err)
	}
	if _, err := db2.Exec(`DELETE FROM schema_migrations WHERE version = 4`); err != nil {
		t.Fatal(err)
	}
	if err := db2.Close(); err != nil {
		t.Fatal(err)
	}

	// 重新打开：migrate 重建 FTS 并回填存量 runs
	db3, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db3.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		rows, qErr := db3.Query(`SELECT run_id FROM runs_fts WHERE runs_fts MATCH ?`, `"backfill"`)
		if qErr == nil {
			var ids []string
			for rows.Next() {
				var id string
				_ = rows.Scan(&id)
				ids = append(ids, id)
			}
			rows.Close()
			if len(ids) == 1 && ids[0] == "run_b" {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("backfill did not make existing run searchable: qErr=%v", qErr)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
