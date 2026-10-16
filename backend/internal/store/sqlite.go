package store

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// Open 打开 SQLite（单实例 + WAL + busy_timeout + 外键），并执行幂等迁移。
func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(time.Minute * 10)

	pragmas := []string{
		`PRAGMA journal_mode=WAL;`,
		`PRAGMA busy_timeout=5000;`,
		`PRAGMA foreign_keys=ON;`,
		`PRAGMA synchronous=NORMAL;`,
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	if err := migrate(context.Background(), db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, schemaSQL); err != nil {
		return err
	}
	// v2：runs 加 parent_run_id 列（可空；PRAGMA 守卫，重复开库跳过）。
	// 注意：经 tx 查（Open 设 SetMaxOpenConns(1)，tx 持有唯一连接；对 db 另开连接会死锁）。
	if has, cErr := colExists(ctx, tx, "runs", "parent_run_id"); cErr != nil {
		return cErr
	} else if !has {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE runs ADD COLUMN parent_run_id TEXT`); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(1, ?)`, time.Now().UnixMilli()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(2, ?)`, time.Now().UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}

// colExists 用 PRAGMA table_info 查列是否存在（经 tx 查，复用单连接，避免 MaxOpenConns(1) 死锁）。
func colExists(ctx context.Context, tx *sql.Tx, table, col string) (bool, error) {
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid, notnull, pkey int
		var name, typ string
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pkey); err != nil {
			return false, err
		}
		if name == col {
			return true, nil
		}
	}
	return false, nil
}
