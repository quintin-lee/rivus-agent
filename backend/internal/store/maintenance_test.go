package store

import (
	"path/filepath"
	"testing"
)

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
