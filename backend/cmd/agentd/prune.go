package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"rivus-agent-backend/internal/config"
	"rivus-agent-backend/internal/store"
)

// runPrune 是 `agentd prune` 子命令入口：解析参数、执行清理，错误按退出码终止。
func runPrune(args []string) {
	if err := pruneCore(args); err != nil {
		if err == errPruneUsage {
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "agentd prune: %v\n", err)
		os.Exit(1)
	}
}

// errPruneUsage 表示参数缺失/非法（退出码 2）。
var errPruneUsage = fmt.Errorf("usage: agentd prune -older-than <duration> [-dry-run] [-config <path>]")

// pruneCore 可测核心：删除 updated_at 早于 cutoff 的终态 run（级联事件/步骤/子 run）。
// DB 路径来自 config（AGENT_CONFIG / AGENT_DB_PATH，同 serve/export/backup）。
func pruneCore(args []string) error {
	fs := flag.NewFlagSet("prune", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "配置文件路径（同 serve 模式）")
	olderThan := fs.String("older-than", "", "清理多久之前的终态 run（如 720h、30d 后缀需为 Go duration）")
	dryRun := fs.Bool("dry-run", false, "只统计不删除")
	if err := fs.Parse(args); err != nil {
		return errPruneUsage
	}
	if *olderThan == "" {
		return errPruneUsage
	}
	dur, err := time.ParseDuration(*olderThan)
	if err != nil || dur < 0 {
		return fmt.Errorf("invalid -older-than %q: %w", *olderThan, err)
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()
	ctx := context.Background()
	tasks := store.NewTaskRepo(db)
	cutoff := time.Now().Add(-dur).UnixMilli()
	if *dryRun {
		n, err := tasks.CountPrunable(ctx, cutoff)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "agentd prune: dry-run 将清理 %d 个 run\n", n)
		return nil
	}
	start := time.Now()
	n, err := tasks.PruneRuns(ctx, cutoff)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "agentd prune: 已清理 %d 个 run（耗时 %s）\n", n, time.Since(start).Round(time.Millisecond))
	return nil
}
