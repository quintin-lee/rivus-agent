package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBackupRoundtrip(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "src.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec("CREATE TABLE t(x TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO t VALUES ('a')"); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(dir, "backup.db")
	if err := Backup(db, dest); err != nil {
		t.Fatalf("backup: %v", err)
	}

	// 备份文件是完整可用的 rivus 库：用 store.Open 再次打开并查询。
	bdb, err := Open(dest)
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer func() { _ = bdb.Close() }()
	var n int
	if err := bdb.QueryRow("SELECT count(*) FROM t").Scan(&n); err != nil {
		t.Fatalf("query backup: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 row in backup, got %d", n)
	}
}

func TestBackupRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "src.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	dest := filepath.Join(dir, "exists.db")
	if err := os.WriteFile(dest, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Backup(db, dest); err == nil {
		t.Fatal("want error for existing destination")
	}
}

func TestBackupRejectsMemoryDB(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := Backup(db, filepath.Join(t.TempDir(), "b.db")); err == nil {
		t.Fatal("want error for :memory: source")
	}
}

func TestStatsAndCheckpoint(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "maint.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.Exec("CREATE TABLE t(x TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO t VALUES ('a')"); err != nil {
		t.Fatal(err)
	}

	st, err := Stats(db)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if st["page_count"] <= 0 || st["page_size"] <= 0 {
		t.Fatalf("page stats wrong: %v", st)
	}
	if st["freelist_count"] < 0 || st["file_bytes"] <= 0 {
		t.Fatalf("file stats wrong: %v", st)
	}
	if st["wal_bytes"] <= 0 {
		t.Fatalf("wal should be non-empty after write: %v", st)
	}

	if err := Checkpoint(db); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	st2, err := Stats(db)
	if err != nil {
		t.Fatal(err)
	}
	if st2["wal_bytes"] != 0 {
		t.Fatalf("wal should be truncated to 0, got %d", st2["wal_bytes"])
	}
}

func TestStatsMemoryDB(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	st, err := Stats(db)
	if err != nil {
		t.Fatal(err)
	}
	if st["wal_bytes"] != 0 || st["file_bytes"] != 0 {
		t.Fatalf("memory db should have 0 file/wal bytes: %v", st)
	}
}
