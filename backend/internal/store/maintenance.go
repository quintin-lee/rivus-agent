package store

import (
	"database/sql"
	"fmt"
	"os"
)

// Stats 返回 SQLite 文件级统计（页与 WAL 字节数），用于启动与诊断日志。
func Stats(db *sql.DB) (map[string]int64, error) {
	out := make(map[string]int64, 5)
	for _, p := range []string{"page_count", "page_size", "freelist_count"} {
		var v int64
		if err := db.QueryRow("PRAGMA " + p).Scan(&v); err != nil {
			return nil, fmt.Errorf("pragma %s: %w", p, err)
		}
		out[p] = v
	}
	path := dbFilePath(db)
	out["file_bytes"] = fileSize(path)
	if path == "" { // :memory: 等无文件 DB
		out["wal_bytes"] = 0
		return out, nil
	}
	out["wal_bytes"] = fileSize(path + "-wal")
	return out, nil
}

// Checkpoint 截断 WAL（TRUNCATE）并刷新查询规划器统计；用于优雅退出。
func Checkpoint(db *sql.DB) error {
	if _, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return fmt.Errorf("wal checkpoint: %w", err)
	}
	if _, err := db.Exec("PRAGMA optimize"); err != nil {
		return fmt.Errorf("optimize: %w", err)
	}
	return nil
}

func dbFilePath(db *sql.DB) string {
	rows, err := db.Query("PRAGMA database_list")
	if err != nil {
		return ""
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var seq int
		var name, file string
		if err := rows.Scan(&seq, &name, &file); err == nil && name == "main" {
			return file
		}
	}
	return ""
}

func fileSize(p string) int64 {
	fi, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return fi.Size()
}
