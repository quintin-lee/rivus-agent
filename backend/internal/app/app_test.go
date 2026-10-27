package app

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"rivus-agent-backend/internal/store"
)

func TestStopCheckpointsAndCloses(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "stop.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec("CREATE TABLE t(x)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO t VALUES ('a')"); err != nil {
		t.Fatal(err)
	}

	a := &App{log: slog.New(slog.NewTextHandler(io.Discard, nil)), db: db}
	if err := a.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}

	// WAL 应被截断：TRUNCATE 置 0；最后一连接干净关闭时 SQLite 会直接删除 -wal 文件
	var walSize int64 = 0
	if fi, err := os.Stat(dbPath + "-wal"); err == nil {
		walSize = fi.Size()
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat wal: %v", err)
	}
	if walSize != 0 {
		t.Fatalf("wal not truncated: %d", walSize)
	}
	// 已关闭的 db 再执行查询应报错（证明 Close 生效）
	if _, err := db.Query("SELECT 1"); err == nil {
		t.Fatal("db should be closed")
	}
}
