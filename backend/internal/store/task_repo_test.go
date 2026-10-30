package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
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

	runs, err := tasks.ListRunsByOwner(ctx, "u1", "", "", 100, 0, "")
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
	small, err := tasks.ListRunsByOwner(ctx, "u1", "", "", 0, 0, "")
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
	got, err := tasks.ListRunsByOwner(ctx, "u1", string(domain.RunFailed), "", 0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "run_bad" {
		t.Fatalf("owner failed filter = %v, want [run_bad]", ids(got))
	}
	// owner 分支：空 status = 不过滤
	got, err = tasks.ListRunsByOwner(ctx, "u1", "", "", 0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("owner no filter = %d runs, want 3", len(got))
	}
	// session 分支：status 过滤 + 空参
	got, err = tasks.ListRunsBySession(ctx, "u1", ses, string(domain.RunCancelled), "", 0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "run_gone" {
		t.Fatalf("session cancelled filter = %v, want [run_gone]", ids(got))
	}
	got, err = tasks.ListRunsBySession(ctx, "u1", ses, "", "", 0, 0, "")
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

func TestListSessionsSearch(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	tasks := NewTaskRepo(db)
	for _, title := range []string{"Alpha 项目", "beta notes", "GAMMA"} {
		if _, err := tasks.CreateSession(ctx, "u1", title); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name string
		q    string
		want []string
	}{
		{"blank q returns all", "   ", []string{"Alpha 项目", "beta notes", "GAMMA"}},
		{"case-insensitive hit", "alpha", []string{"Alpha 项目"}},
		{"uppercase term hits lowercase", "BETA", []string{"beta notes"}},
		{"miss returns empty", "zzz", []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tasks.ListSessions(ctx, "u1", tc.q, 0, 0, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d sessions (%v), want %d", len(got), got, len(tc.want))
			}
			for _, s := range got {
				ok := false
				for _, w := range tc.want {
					if s.Title == w {
						ok = true
					}
				}
				if !ok {
					t.Fatalf("unexpected session %q", s.Title)
				}
			}
		})
	}
}

func TestListSessionsOffset(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	tasks := NewTaskRepo(db)
	for i := 1; i <= 5; i++ {
		if _, err := tasks.CreateSession(ctx, "u1", fmt.Sprintf("ses-%02d", i)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond) // ms 精度：确保 updated_at 递增可分页
	}
	// page 1: limit 2 offset 0
	got, err := tasks.ListSessions(ctx, "u1", "", 2, 0, "")
	if err != nil || len(got) != 2 {
		t.Fatalf("page1: n=%d err=%v", len(got), err)
	}
	if got[0].Title != "ses-05" {
		t.Fatalf("page1 first = %q, want ses-05 (updated_at DESC)", got[0].Title)
	}
	// page 2
	got, err = tasks.ListSessions(ctx, "u1", "", 2, 2, "")
	if err != nil || len(got) != 2 {
		t.Fatalf("page2: n=%d err=%v", len(got), err)
	}
	if got[0].Title != "ses-03" {
		t.Fatalf("page2 first = %q, want ses-03", got[0].Title)
	}
	// page 3: last one
	got, err = tasks.ListSessions(ctx, "u1", "", 2, 4, "")
	if err != nil || len(got) != 1 {
		t.Fatalf("page3: n=%d err=%v", len(got), err)
	}
	// beyond
	got, err = tasks.ListSessions(ctx, "u1", "", 2, 5, "")
	if err != nil || len(got) != 0 {
		t.Fatalf("beyond: n=%d err=%v", len(got), err)
	}
}

func TestListRunsSearch(t *testing.T) {
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
	mkRun := func(id, goal string) {
		run := &domain.Run{ID: id, SessionID: ses, OwnerID: "u1", Status: domain.RunSucceeded,
			Mode: "react", Goal: goal,
			Budget:  domain.Budget{MaxDurationSeconds: 60, MaxModelCalls: 5, MaxToolCalls: 5, MaxIterations: 3, MaxOutputBytes: 1000},
			Attempt: 1}
		if err := tasks.CreateRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	mkRun("run_a", "Alpha 任务")
	mkRun("run_b", "beta notes")
	mkRun("run_c", "gamma task")
	if err := tasks.SetResult(ctx, "run_c", `{"summary":"SECRET-RESULT-MARKER 报告"}`); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		q    string
		want []string
	}{
		{"blank q returns all", "   ", []string{"run_c", "run_b", "run_a"}},
		{"case-insensitive hit", "alpha", []string{"run_a"}},
		{"uppercase term hits lowercase", "BETA", []string{"run_b"}},
		{"result-only hit", "secret-result-marker", []string{"run_c"}},
		{"miss returns empty", "zzz", []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/owner", func(t *testing.T) {
			got, err := tasks.ListRunsByOwner(ctx, "u1", "", tc.q, 0, 0, "")
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(ids(got), ",") != strings.Join(tc.want, ",") {
				t.Fatalf("owner search %q = %v, want %v", tc.q, ids(got), tc.want)
			}
		})
		t.Run(tc.name+"/session", func(t *testing.T) {
			got, err := tasks.ListRunsBySession(ctx, "u1", ses, "", tc.q, 0, 0, "")
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(ids(got), ",") != strings.Join(tc.want, ",") {
				t.Fatalf("session search %q = %v, want %v", tc.q, ids(got), tc.want)
			}
		})
	}
}

func TestSessionsCursorPagination(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	tasks := NewTaskRepo(db)
	for i := 1; i <= 4; i++ {
		if _, err := tasks.CreateSession(ctx, "u1", fmt.Sprintf("p%d", i)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond) // updated_at ms 精度：保证游标键严格递减
	}
	all, err := tasks.ListSessions(ctx, "u1", "", 0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	wantAll := []string{"p4", "p3", "p2", "p1"}
	for i, s := range all {
		if s.Title != wantAll[i] {
			t.Fatalf("full order[%d] = %s, want %s", i, s.Title, wantAll[i])
		}
	}

	page1, err := tasks.ListSessions(ctx, "u1", "", 2, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 2 || page1[0].Title != "p4" || page1[1].Title != "p3" {
		t.Fatalf("page1 = %v", page1)
	}

	cur := EncodeCursor(page1[1].UpdatedAt, page1[1].ID)
	page2, err := tasks.ListSessions(ctx, "u1", "", 2, 0, cur)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 2 || page2[0].Title != "p2" || page2[1].Title != "p1" {
		t.Fatalf("page2 = %v", page2)
	}

	if sv, id, err := DecodeCursor(cur); err != nil || id != page1[1].ID || sv != page1[1].UpdatedAt {
		t.Fatalf("cursor roundtrip = %d,%s,%v", sv, id, err)
	}
	if _, _, err := DecodeCursor("!!!bad"); err == nil {
		t.Fatal("invalid cursor should error")
	}
}

func TestRunsCursorPagination(t *testing.T) {
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
	mk := func(id string) {
		run := &domain.Run{ID: id, SessionID: ses, OwnerID: "u1", Status: domain.RunSucceeded,
			Mode: "react", Goal: "g",
			Budget:  domain.Budget{MaxDurationSeconds: 60, MaxModelCalls: 5, MaxToolCalls: 5, MaxIterations: 3, MaxOutputBytes: 1000},
			Attempt: 1}
		if err := tasks.CreateRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond) // created_at ms 精度
	}
	mk("run_c1")
	mk("run_c2")
	mk("run_c3")

	page1, err := tasks.ListRunsByOwner(ctx, "u1", "", "", 2, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 2 || page1[0].ID != "run_c3" {
		t.Fatalf("runs page1 = %v", ids(page1))
	}
	cur := EncodeCursor(page1[1].CreatedAt, page1[1].ID)
	page2, err := tasks.ListRunsByOwner(ctx, "u1", "", "", 2, 0, cur)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 1 || page2[0].ID != "run_c1" {
		t.Fatalf("runs page2 = %v", ids(page2))
	}

	// 会话内列表同样支持
	page1s, err := tasks.ListRunsBySession(ctx, "u1", ses, "", "", 2, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page1s) != 2 || page1s[0].ID != "run_c3" {
		t.Fatalf("session page1 = %v", ids(page1s))
	}
	cur2 := EncodeCursor(page1s[1].CreatedAt, page1s[1].ID)
	page2s, err := tasks.ListRunsBySession(ctx, "u1", ses, "", "", 2, 0, cur2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2s) != 1 || page2s[0].ID != "run_c1" {
		t.Fatalf("session page2 = %v", ids(page2s))
	}
}

func TestRunsFTSQuery(t *testing.T) {
	if _, _, ok := runsFTSQuery("ab"); ok {
		t.Fatal("short term should fall back to LIKE")
	}
	sql, args, ok := runsFTSQuery("alpha")
	if !ok {
		t.Fatal("long term should use FTS")
	}
	if !strings.Contains(sql, "runs_fts MATCH") {
		t.Fatalf("sql = %q", sql)
	}
	if len(args) != 1 || args[0] != `"alpha"` {
		t.Fatalf("args = %v", args)
	}
	_, args, _ = runsFTSQuery(`a"b`)
	if args[0] != `"a""b"` {
		t.Fatalf("quote not escaped: %v", args)
	}
}

func TestListRunsSearchFTSParity(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	tasks := NewTaskRepo(db)
	ses, err := tasks.CreateSession(ctx, "u1", "p")
	if err != nil {
		t.Fatal(err)
	}
	ftsRun(t, tasks, ses, "parity_1", "整理 Alpha 资料")
	ftsRun(t, tasks, ses, "parity_2", `say "quoted" word`)
	ftsRun(t, tasks, ses, "parity_3", "ab shortcut")

	cases := []struct {
		name string
		q    string
		want string
	}{
		{"cjk long term goes FTS", "理 alpha", "parity_1"},
		{"quote-escaped phrase", `"quoted"`, "parity_2"},
		{"short ascii falls back to LIKE", "ab", "parity_3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tasks.ListRunsByOwner(ctx, "u1", "", tc.q, 50, 0, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].ID != tc.want {
				t.Fatalf("q=%q got %v, want [%s]", tc.q, got, tc.want)
			}
		})
	}
}

func TestPruneRuns(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	tasks := NewTaskRepo(db)
	ses, err := tasks.CreateSession(ctx, "u1", "prune-ses")
	if err != nil {
		t.Fatal(err)
	}

	seed := func(id, status string, parent string) {
		run := &domain.Run{ID: id, SessionID: ses, OwnerID: "u1", Status: domain.RunStatus(status),
			Mode: "react", Goal: "g-" + id, ParentRunID: parent,
			Budget:  domain.Budget{MaxDurationSeconds: 60, MaxModelCalls: 5, MaxToolCalls: 5, MaxIterations: 3, MaxOutputBytes: 1000},
			Attempt: 1}
		if err := tasks.CreateRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	seed("run_old", "succeeded", "")
	seed("run_new", "succeeded", "")
	seed("run_act", "running", "")
	seed("run_p", "failed", "")
	seed("run_c", "succeeded", "run_p")

	events := NewEventRepo(db)
	if _, err := events.Append(ctx, "run_old", domain.EvtRunStarted, "{}", "normal"); err != nil {
		t.Fatal(err)
	}
	if err := tasks.CreateSteps(ctx, "run_old", []domain.Step{{ID: "st1", Index: 0, Description: "d", Status: domain.StepSucceeded}}); err != nil {
		t.Fatal(err)
	}
	if _, err := events.Append(ctx, "run_p", domain.EvtRunStarted, "{}", "normal"); err != nil {
		t.Fatal(err)
	}

	// 回拨“旧”runs 的 updated_at 到固定小值（CreateRun 内部写 nowMs，不可注入）
	for _, id := range []string{"run_old", "run_p", "run_c", "run_act"} {
		if _, err := db.ExecContext(ctx, `UPDATE runs SET updated_at = 1000 WHERE id = ?`, id); err != nil {
			t.Fatal(err)
		}
	}
	// run_new 保持 nowMs（远大于 cutoff=2000）

	// 记忆挂在 session 上，应不受 prune 影响
	memories := NewMemoryRepo(db)
	if err := memories.Write(ctx, &domain.Memory{SessionID: ses, OwnerID: "u1", Content: "keep-me"}); err != nil {
		t.Fatal(err)
	}

	n, err := tasks.PruneRuns(ctx, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("pruned = %d, want 3 (run_old, run_p, run_c)", n)
	}
	for _, gone := range []string{"run_old", "run_p", "run_c"} {
		if _, err := tasks.GetRun(ctx, "u1", gone); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("%s should be pruned, err = %v", gone, err)
		}
	}
	for _, keep := range []string{"run_new", "run_act"} {
		if _, err := tasks.GetRun(ctx, "u1", keep); err != nil {
			t.Fatalf("%s should survive, err = %v", keep, err)
		}
	}
	var cnt int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_events WHERE run_id IN ('run_old','run_p')`).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 0 {
		t.Fatalf("events cascade failed, %d rows left", cnt)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_steps WHERE run_id = 'run_old'`).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 0 {
		t.Fatalf("steps cascade failed, %d rows left", cnt)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM session_memories WHERE session_id = ?`, ses).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 1 {
		t.Fatalf("memory should survive prune, got %d", cnt)
	}
}
