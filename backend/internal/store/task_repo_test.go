package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"rivus-agent-backend/internal/domain"
)

func setupRun(t *testing.T, tasks *TaskRepo, ses, runID string, status domain.RunStatus) {
	t.Helper()
	run := &domain.Run{ID: runID, SessionID: ses, OwnerID: "u1", Status: status,
		Mode: "react", Goal: "g",
		Budget:  domain.Budget{MaxDurationSeconds: 60, MaxModelCalls: 5, MaxToolCalls: 5, MaxIterations: 3, MaxOutputBytes: 1000},
		Attempt: 1}
	if err := tasks.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteSessionCascade(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	tasks := NewTaskRepo(db)
	events := NewEventRepo(db)
	ses, err := tasks.CreateSession(ctx, "u1", "t")
	if err != nil {
		t.Fatal(err)
	}
	setupRun(t, tasks, ses, "run_1", domain.RunQueued)
	_ = tasks.UpdateStatus(ctx, "u1", "run_1", domain.RunCancelled, "", "")
	if _, err := events.Append(ctx, "run_1", domain.EvtRunStarted, `{}`, ""); err != nil {
		t.Fatal(err)
	}
	if err := tasks.DeleteSession(ctx, "u1", ses); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := tasks.GetSession(ctx, "u1", ses); err == nil {
		t.Fatal("session must be gone")
	}
	if evs, _ := events.ListAfter(ctx, "run_1", 0, 10); len(evs) != 0 {
		t.Fatal("events must be gone")
	}
	if err := tasks.DeleteSession(ctx, "u1", ses); err == nil {
		t.Fatal("second delete must fail")
	}
}

func TestDeleteSessionRefusesActiveRun(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	tasks := NewTaskRepo(db)
	ses, _ := tasks.CreateSession(ctx, "u1", "t")
	setupRun(t, tasks, ses, "run_1", domain.RunRunning)
	if err := tasks.DeleteSession(ctx, "u1", ses); err != domain.ErrConflict {
		t.Fatalf("want ErrConflict, got %v", err)
	}
}

func TestDeleteSessionWrongOwner(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	tasks := NewTaskRepo(db)
	ses, _ := tasks.CreateSession(ctx, "u1", "t")
	if err := tasks.DeleteSession(ctx, "intruder", ses); err != domain.ErrForbidden {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
}

func TestListChildrenByRun(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	tasks := NewTaskRepo(db)
	ses, err := tasks.CreateSession(ctx, "u1", "sub")
	if err != nil {
		t.Fatal(err)
	}
	// 顶层父
	if err := tasks.CreateRun(ctx, &domain.Run{ID: "p1", SessionID: ses, OwnerID: "u1", Status: domain.RunQueued, Mode: "react", Goal: "g", Budget: domain.Budget{}, Attempt: 1}); err != nil {
		t.Fatal(err)
	}
	// 子行（parent_run_id=p1）
	if err := tasks.CreateRun(ctx, &domain.Run{ID: "p1c", SessionID: ses, OwnerID: "u1", Status: domain.RunQueued, Mode: "react", Goal: "cg", Budget: domain.Budget{}, ParentRunID: "p1", Attempt: 1}); err != nil {
		t.Fatal(err)
	}
	kids, err := tasks.ListChildrenByRun(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(kids) != 1 || kids[0].ID != "p1c" {
		t.Fatalf("want 1 child p1c, got %+v", kids)
	}
	// 未知 parent → 非 nil 空切片
	empty, err := tasks.ListChildrenByRun(ctx, "nope")
	if err != nil {
		t.Fatal(err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("want non-nil empty [] got %v", empty)
	}
}

func TestListRunsByOwner(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	tasks := NewTaskRepo(db)

	ses1, err := tasks.CreateSession(ctx, "u1", "s1")
	if err != nil {
		t.Fatal(err)
	}
	ses2, err := tasks.CreateSession(ctx, "u1", "s2")
	if err != nil {
		t.Fatal(err)
	}

	// 三个顶层 run（跨两个会话）+ 一个子 run（应被排除）
	setupRun(t, tasks, ses1, "run_a", domain.RunSucceeded)
	setupRun(t, tasks, ses2, "run_b", domain.RunFailed)
	setupRun(t, tasks, ses1, "run_c", domain.RunQueued)
	if err := tasks.CreateRun(ctx, &domain.Run{ID: "run_child", SessionID: ses1, OwnerID: "u1",
		Status: domain.RunSucceeded, Mode: "react", Goal: "child", ParentRunID: "run_a", Attempt: 1}); err != nil {
		t.Fatal(err)
	}

	runs, err := tasks.ListRunsByOwner(ctx, "u1", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 3 {
		t.Fatalf("want 3 top-level runs, got %d", len(runs))
	}
	// created_at 相同时 SQLite 返回插入序的倒序不定 —— 只断言集合与排除
	ids := map[string]bool{}
	for _, r := range runs {
		ids[r.ID] = true
		if r.ParentRunID != "" {
			t.Fatalf("child run %s leaked into owner history", r.ID)
		}
	}
	for _, want := range []string{"run_a", "run_b", "run_c"} {
		if !ids[want] {
			t.Fatalf("missing run %s in %v", want, ids)
		}
	}
	if ids["run_child"] {
		t.Fatal("child run must be excluded")
	}

	// limit 钳制
	small, err := tasks.ListRunsByOwner(ctx, "u1", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(small) != 3 { // 3 条 < 50，钳制不影响结果
		t.Fatalf("limit=0 should still return all 3, got %d", len(small))
	}
}

func TestListRunsFilterByStatus(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	tasks := NewTaskRepo(db)
	ses, err := tasks.CreateSession(ctx, "u1", "s")
	if err != nil {
		t.Fatal(err)
	}
	setupRun(t, tasks, ses, "run_ok", domain.RunSucceeded)
	setupRun(t, tasks, ses, "run_bad", domain.RunFailed)
	setupRun(t, tasks, ses, "run_gone", domain.RunCancelled)

	// owner 分支：status 过滤
	got, err := tasks.ListRunsByOwner(ctx, "u1", string(domain.RunFailed), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "run_bad" {
		t.Fatalf("owner failed filter = %v, want [run_bad]", ids(got))
	}
	// owner 分支：空 status = 不过滤
	got, err = tasks.ListRunsByOwner(ctx, "u1", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("owner no filter = %d runs, want 3", len(got))
	}
	// session 分支：status 过滤 + 空参
	got, err = tasks.ListRunsBySession(ctx, "u1", ses, string(domain.RunCancelled), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "run_gone" {
		t.Fatalf("session cancelled filter = %v, want [run_gone]", ids(got))
	}
	got, err = tasks.ListRunsBySession(ctx, "u1", ses, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("session no filter = %d runs, want 3", len(got))
	}
}

func ids(runs []domain.Run) []string {
	out := make([]string, 0, len(runs))
	for _, r := range runs {
		out = append(out, r.ID)
	}
	return out
}

func TestRenameSession(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	tasks := NewTaskRepo(db)
	ses, err := tasks.CreateSession(ctx, "u1", "old")
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, uaBefore, err := tasks.GetSession(ctx, "u1", ses)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond) // ms 精度：确保 updated_at 可观测推进
	if err := tasks.RenameSession(ctx, "u1", ses, "new-title"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	_, title, _, uaAfter, err := tasks.GetSession(ctx, "u1", ses)
	if err != nil {
		t.Fatal(err)
	}
	if title != "new-title" {
		t.Fatalf("title = %q, want new-title", title)
	}
	if uaAfter < uaBefore {
		t.Fatalf("updated_at went backwards: %d < %d", uaAfter, uaBefore)
	}
	if err := tasks.RenameSession(ctx, "someone-else", ses, "x"); err != domain.ErrForbidden {
		t.Fatalf("owner mismatch err = %v", err)
	}
	if err := tasks.RenameSession(ctx, "u1", "ses_missing", "x"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing err = %v", err)
	}
}
