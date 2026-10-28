package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"rivus-agent-backend/internal/app"
)

func main() {
	// 子命令分发：`agentd export ...` 走导出，其余按服务模式启动。
	if len(os.Args) > 1 && os.Args[1] == "export" {
		runExport(os.Args[2:])
		return
	}
	runServe()
}

func runServe() {
	cfgPath := flag.String("config", os.Getenv("AGENT_CONFIG"), "config file path (optional)")
	flag.Parse()
	a, err := app.New(*cfgPath)
	if err != nil {
		slog.Error("init failed", "err", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = a.Stop(shut)
	}()
	if err := a.Start(ctx); err != nil {
		slog.Error("server exited", "err", err)
		os.Exit(1)
	}
}
