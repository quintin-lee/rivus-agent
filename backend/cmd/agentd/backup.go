package main

import (
	"flag"
	"fmt"
	"os"

	"rivus-agent-backend/internal/config"
	"rivus-agent-backend/internal/store"
)

// runBackup 是 `agentd backup` 子命令入口：解析参数、执行备份，错误按退出码终止。
func runBackup(args []string) {
	if err := backupCore(args); err != nil {
		if err == errBackupUsage {
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "agentd backup: %v\n", err)
		os.Exit(1)
	}
}

// errBackupUsage 表示参数缺失/非法（退出码 2）。
var errBackupUsage = fmt.Errorf("usage: agentd backup -o <dest-path> [-config <path>]")

// backupCore 可测核心：-o 必填；DB 路径来自 config（AGENT_CONFIG / AGENT_DB_PATH，同 serve/export）。
func backupCore(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "配置文件路径（同 serve 模式）")
	out := fs.String("o", "", "备份目标文件路径（必填；已存在则拒绝覆盖）")
	if err := fs.Parse(args); err != nil {
		return errBackupUsage
	}
	if *out == "" {
		return errBackupUsage
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
	if err := store.Backup(db, *out); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "agentd backup: 已备份到 %s\n", *out)
	return nil
}
