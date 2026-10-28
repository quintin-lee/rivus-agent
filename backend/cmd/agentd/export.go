package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"rivus-agent-backend/internal/config"
	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/store"
)

// exportPayload 是单个会话的完整导出结构（runs / events / memories）。
type exportPayload struct {
	SessionID string              `json:"session_id"`
	Runs      []domain.Run        `json:"runs"`
	Events    []domain.AgentEvent `json:"events"`
	Memories  []domain.Memory     `json:"memories"`
}

// buildExportPayload 从数据库读取一个会话的全部数据。会话不存在时返回错误。
func buildExportPayload(db *sql.DB, sessionID string) (*exportPayload, error) {
	ctx := context.Background()
	tasks := store.NewTaskRepo(db)
	events := store.NewEventRepo(db)
	memories := store.NewMemoryRepo(db)

	var ownerID string
	if err := db.QueryRowContext(ctx,
		`SELECT owner_id FROM sessions WHERE id = ?`, sessionID).Scan(&ownerID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("session %q not found", sessionID)
		}
		return nil, fmt.Errorf("lookup session owner: %w", err)
	}
	if _, _, _, _, err := tasks.GetSession(ctx, ownerID, sessionID); err != nil {
		return nil, fmt.Errorf("load session: %w", err)
	}

	// ListRunsBySession 会把 limit 钳制到 <=100，这里取满一页；
	// 超过 100 条时由 runExport 打印警告（当前单会话超过 100 个 run 极少见）。
	runs, err := tasks.ListRunsBySession(ctx, ownerID, sessionID, 100)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	payload := &exportPayload{SessionID: sessionID, Runs: runs}
	for _, r := range runs {
		evs, err := events.ListAfter(ctx, r.ID, 0, -1) // -1 = 不限制
		if err != nil {
			return nil, fmt.Errorf("list events for run %s: %w", r.ID, err)
		}
		payload.Events = append(payload.Events, evs...)
	}
	mems, err := memories.Latest(ctx, sessionID, 1<<20)
	if err != nil {
		return nil, fmt.Errorf("list memories: %w", err)
	}
	payload.Memories = mems
	return payload, nil
}

// runExport 处理 `agentd export` 子命令。
func runExport(args []string) {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "配置文件路径（同 serve 模式）")
	sessionID := fs.String("session", "", "要导出的会话 ID（必填）")
	out := fs.String("o", "", "输出文件路径（缺省写到 stdout）")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if *sessionID == "" {
		fmt.Fprintln(os.Stderr, "agentd export: -session 必填")
		os.Exit(2)
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agentd export: load config: %v\n", err)
		os.Exit(2)
	}
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agentd export: open db: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	payload, err := buildExportPayload(db, *sessionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agentd export: %v\n", err)
		os.Exit(1)
	}
	if len(payload.Runs) >= 100 {
		fmt.Fprintln(os.Stderr, "agentd export: 警告：run 数达到单页上限 100，导出可能不完整（如有更多 run 请分批处理）")
	}

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "agentd export: marshal: %v\n", err)
		os.Exit(1)
	}
	if *out == "" {
		os.Stdout.Write(data)
		fmt.Println()
		return
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "agentd export: write %s: %v\n", *out, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "agentd export: 已写入 %s（%d runs, %d events, %d memories）\n",
		*out, len(payload.Runs), len(payload.Events), len(payload.Memories))
}
