package app

import (
	"context"
	"database/sql"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	einomodel "github.com/cloudwego/eino/components/model"
	httpapi "rivus-agent-backend/internal/api/http"
	"rivus-agent-backend/internal/config"
	"rivus-agent-backend/internal/model"
	"rivus-agent-backend/internal/observability"
	"rivus-agent-backend/internal/runtime"
	"rivus-agent-backend/internal/service"
	"rivus-agent-backend/internal/skill"
	"rivus-agent-backend/internal/store"
	"rivus-agent-backend/internal/tool"
	"rivus-agent-backend/internal/tool/builtin"
)

type App struct {
	cfg config.Config
	log *slog.Logger
	srv *http.Server
	db  *sql.DB
}

func New(cfgPath string) (*App, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	return &App{cfg: cfg, log: observability.NewLogger(cfg.LogLevel)}, nil
}

func (a *App) Start(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(a.cfg.DBPath), 0o755); err != nil {
		return err
	}
	db, err := store.Open(a.cfg.DBPath)
	if err != nil {
		return err
	}
	tasks := store.NewTaskRepo(db)
	events := store.NewEventRepo(db)
	approvals := store.NewApprovalRepo(db)
	checkpoints := store.NewCheckpointStore(db)

	registry := tool.NewRegistry()
	if err := builtin.RegisterAll(registry); err != nil {
		return err
	}
	executor := tool.NewExecutor(registry)
	cfg := a.cfg
	runner := runtime.NewEinoRunner(func() (einomodel.ToolCallingChatModel, error) {
		return model.FromConfig(cfg)
	}, registry, executor, checkpoints)
	rt := runtime.New(runtime.Deps{Tasks: tasks, Events: events, Approvals: approvals, Runner: runner}, cfg.WorkerConcurrency)

	if _, err := runtime.RecoverOrphans(ctx, tasks, events); err != nil {
		a.log.Warn("orphan recovery failed", "err", err)
	}
	_ = skill.NewLoader(cfg.SkillsDir).Refresh()

	runs := service.NewRunService(cfg, tasks, events, approvals, rt, runner)
	sessions := service.NewSessionService(tasks)
	settings := store.NewSettingsRepo(db)
	srv := httpapi.New(cfg, db, tasks, events, settings, runs, sessions)

	httpSrv := &http.Server{
		Addr: cfg.Addr, Handler: srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	a.srv = httpSrv
	a.db = db
	a.log.Info("agentd listening", "addr", cfg.Addr)
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (a *App) Stop(ctx context.Context) error {
	if a.srv != nil {
		_ = a.srv.Shutdown(ctx)
	}
	if a.db != nil {
		return a.db.Close()
	}
	return nil
}
