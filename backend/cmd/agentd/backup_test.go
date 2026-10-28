package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/store"
)

// newBackupDB 建一个含会话与 run 的文件库（备份源），并把它设为 AGENT_DB_PATH。
func newBackupDB(t *testing.T) (srcPath, ownerID, sesID string) {
	t.Helper()
	srcPath = filepath.Join(t.TempDir(), "src.db")
	t.Setenv("AGENT_DB_PATH", srcPath)
	db, err := store.Open(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tasks := store.NewTaskRepo(db)
	ses, err := tasks.CreateSession(context.Background(), "u1", "backup-src")
	if err != nil {
		t.Fatal(err)
	}
	run := &domain.Run{ID: "run_bak", SessionID: ses, OwnerID: "u1",
		Status: domain.RunSucceeded, Mode: "react", Goal: "g", Attempt: 1}
	if err := tasks.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	return srcPath, "u1", ses
}

func TestBackupCoreCreatesQueryableSnapshot(t *testing.T) {
	_, ownerID, sesID := newBackupDB(t)
	dest := filepath.Join(t.TempDir(), "snap.db")

	if err := backupCore([]string{"-o", dest}); err != nil {
		t.Fatalf("backupCore: %v", err)
	}

	// 重新打开备份文件，数据可查询
	snap, err := store.Open(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	tasks := store.NewTaskRepo(snap)
	if _, _, _, _, err := tasks.GetSession(context.Background(), ownerID, sesID); err != nil {
		t.Fatalf("session not queryable in snapshot: %v", err)
	}
}

func TestBackupCoreRefusesExistingDest(t *testing.T) {
	newBackupDB(t)
	dest := filepath.Join(t.TempDir(), "exists.db")
	if err := os.WriteFile(dest, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := backupCore([]string{"-o", dest})
	if err == nil || !strings.Contains(err.Error(), "exists") {
		t.Fatalf("err = %v, want destination exists error", err)
	}
}

func TestBackupCoreRequiresOutput(t *testing.T) {
	newBackupDB(t)
	err := backupCore([]string{})
	if err == nil || !strings.Contains(err.Error(), "-o") {
		t.Fatalf("err = %v, want -o required error", err)
	}
}
