package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"rivus-agent-backend/internal/domain"
)

func newMemDB(t *testing.T) (*sql.DB, *MemoryRepo) {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "mem.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, NewMemoryRepo(db)
}

func TestMemoryWriteAndLatest(t *testing.T) {
	db, repo := newMemDB(t)
	ctx := context.Background()
	tasks := NewTaskRepo(db)
	ses, err := tasks.CreateSession(ctx, "o1", "t1")
	if err != nil {
		t.Fatal(err)
	}
	otherSes, err := tasks.CreateSession(ctx, "o1", "t2")
	if err != nil {
		t.Fatal(err)
	}
	m := &domain.Memory{SessionID: ses, OwnerID: "o1", Content: "用户偏好简洁回复", SourceRunID: "run_1"}
	if err := repo.Write(ctx, m); err != nil {
		t.Fatal(err)
	}
	if m.ID == "" || m.CreatedAt == 0 {
		t.Fatal("Write must fill ID and CreatedAt")
	}
	got, err := repo.Latest(ctx, ses, 10)
	if err != nil || len(got) != 1 || got[0].Content != "用户偏好简洁回复" {
		t.Fatalf("got=%v err=%v", got, err)
	}
	// 其他会话不可见
	other, _ := repo.Latest(ctx, otherSes, 10)
	if len(other) != 0 {
		t.Fatal("session isolation broken")
	}
}

func TestMemoryLRUTrim(t *testing.T) {
	db, repo := newMemDB(t)
	ctx := context.Background()
	ses, err := NewTaskRepo(db).CreateSession(ctx, "o", "t")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 105; i++ {
		_ = repo.Write(ctx, &domain.Memory{SessionID: ses, OwnerID: "o", Content: fmt.Sprintf("m%03d", i)})
	}
	got, err := repo.Latest(ctx, ses, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 100 {
		t.Fatalf("want 100 after LRU trim, got %d", len(got))
	}
	// 最旧的 5 条应被淘汰；最新的 m104 应在最前
	if got[0].Content != "m104" {
		t.Fatalf("newest first broken: %q", got[0].Content)
	}
	for _, m := range got {
		if m.Content == "m000" {
			t.Fatal("oldest memory should have been evicted")
		}
	}
}

func TestMemoryCascadeOnSessionDelete(t *testing.T) {
	db, repo := newMemDB(t)
	ctx := context.Background()
	tasks := NewTaskRepo(db)
	ses, err := tasks.CreateSession(ctx, "o1", "t")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Write(ctx, &domain.Memory{SessionID: ses, OwnerID: "o1", Content: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := tasks.DeleteSession(ctx, "o1", ses); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.Latest(ctx, ses, 10)
	if len(got) != 0 {
		t.Fatal("memories must cascade-delete with session")
	}
}
