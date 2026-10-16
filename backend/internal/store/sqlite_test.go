package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

// TestMigrationAddsParentRunIdIdempotently 验证 v2 迁移（runs.parent_run_id）在文件库上两次 Open 幂等。
func TestMigrationAddsParentRunIdIdempotently(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "agent.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if err := hasColumn(db, "runs", "parent_run_id"); err != nil {
		t.Fatalf("after first open: %v", err)
	}
	_ = db.Close()
	// 第二次 open：已含该列，须幂等（不报 duplicate column / no such table）。
	db2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("second open (should be idempotent): %v", err)
	}
	defer db2.Close()
	if err := hasColumn(db2, "runs", "parent_run_id"); err != nil {
		t.Fatalf("second open: %v", err)
	}
}

// hasColumn 查 PRAGMA table_info 是否含某列。
func hasColumn(db *sql.DB, table, col string) error {
	rows, err := db.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pkey int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pkey); err != nil {
			return err
		}
		if name == col {
			return nil
		}
	}
	return fmt.Errorf("column %s.%s not found", table, col)
}

