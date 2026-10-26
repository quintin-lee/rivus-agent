package store

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// Open 打开 SQLite（WAL + busy_timeout + 外键），并执行幂等迁移。
func Open(path string) (*sql.DB, error) {
	// 文件库放开 WAL 并发（读写走不同连接）；内存库必须单连接 ——
	// SQLite :memory: 是 per-connection 的，多连接会各持独立空库（test DB 被分片）。
	conns := 8
	if path == ":memory:" || strings.Contains(path, "mode=memory") {
		conns = 1
	}
	dsn := path
	if conns > 1 && !strings.Contains(path, "?") {
		// db.Exec 的 PRAGMA 只作用于执行它的那一条连接；连接池会惰性新建连接，
		// 必须经 DSN _pragma 让每条新连接都带上（否则新连接 busy_timeout=0，
		// 并发写立即 SQLITE_BUSY）。
		dsn = path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)" +
			"&_pragma=synchronous(NORMAL)&_pragma=journal_mode(WAL)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(conns)
	db.SetMaxIdleConns(conns)
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
	// 注意：经 tx 查（与迁移同事务，读到的是本事务视图）。
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
	// v3：session_memories（会话级持久记忆；建表走 schemaSQL 的 IF NOT EXISTS，天然幂等）。
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(3, ?)`, time.Now().UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}

// colExists 用 PRAGMA table_info 查列是否存在（经 tx 查，复用迁移事务视图）。
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
