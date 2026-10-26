package integration

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/store"
)

// 文件库在仍有未提交事务占用连接时，其他连接的写入不得被连接池饿死。
// 旧实现 MaxOpenConns(1) 下该 goroutine 会永久阻塞（池中无空闲连接）。
func TestSQLiteWriteNotBlockedByOpenTx(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "concurrent.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	// agent_events.run_id 有外键，先铺底 run。
	tasks := store.NewTaskRepo(db)
	ses, err := tasks.CreateSession(context.Background(), "o", "conc")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := tasks.CreateRun(context.Background(), &domain.Run{
		ID: "conc-run-1", SessionID: ses, OwnerID: "o", Goal: "g", Mode: "react",
		Status: domain.RunQueued, IdempotencyKey: "idem-conc-1",
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}

	// 持有一个只读事务：占用一条连接，但不持有写锁。
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.QueryContext(context.Background(), "SELECT 1"); err != nil {
		t.Fatalf("tx read: %v", err)
	}

	events := store.NewEventRepo(db)
	done := make(chan error, 1)
	go func() {
		_, err := events.Append(context.Background(), "conc-run-1", domain.EvtRunCreated, `{}`, "normal")
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("append: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Append blocked >2s while a read tx held the only connection — SetMaxOpenConns still 1?")
	}
}

// 8 协程并发 CreateRun + Append + GetRun 全部成功且可读。
func TestSQLiteConcurrentWorkers(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "workers.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	tasks := store.NewTaskRepo(db)
	events := store.NewEventRepo(db)
	ses, err := tasks.CreateSession(context.Background(), "o", "conc")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	const workers = 8
	var wg sync.WaitGroup
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx := context.Background()
			runID := fmt.Sprintf("conc-run-%d", i)
			run := &domain.Run{
				ID: runID, SessionID: ses, OwnerID: "o", Goal: "g", Mode: "react",
				Status: domain.RunQueued, IdempotencyKey: fmt.Sprintf("idem-%d", i),
			}
			if err := tasks.CreateRun(ctx, run); err != nil {
				errs[i] = fmt.Errorf("create: %w", err)
				return
			}
			if _, err := events.Append(ctx, runID, domain.EvtRunCreated, `{}`, "normal"); err != nil {
				errs[i] = fmt.Errorf("append: %w", err)
				return
			}
			if _, err := tasks.GetRun(ctx, "o", runID); err != nil {
				errs[i] = fmt.Errorf("get: %w", err)
			}
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", i, err)
		}
	}
}
