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
	"rivus-agent-backend/internal/eventshub"
	mcp "rivus-agent-backend/internal/mcp"
	"rivus-agent-backend/internal/model"
	"rivus-agent-backend/internal/observability"
	"rivus-agent-backend/internal/runtime"
	"rivus-agent-backend/internal/service"
	"rivus-agent-backend/internal/skill"
	"rivus-agent-backend/internal/store"
	"rivus-agent-backend/internal/subagent"
	"rivus-agent-backend/internal/tool"
	"rivus-agent-backend/internal/tool/builtin"
)

type App struct {
	cfg         config.Config
	log         *slog.Logger
	srv         *http.Server
	db          *sql.DB
	manager     *mcp.Manager
	skillLoader *skill.Loader
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
	hub := eventshub.New()
	events.SetPublisher(hub.Publish)
	approvals := store.NewApprovalRepo(db)
	checkpoints := store.NewCheckpointStore(db)

	registry := tool.NewRegistry()
	if err := builtin.RegisterAll(registry); err != nil {
		return err
	}
	executor := tool.NewExecutor(registry)
	cfg := a.cfg
	settings := store.NewSettingsRepo(db)
	runner := runtime.NewEinoRunner(func() (einomodel.ToolCallingChatModel, error) {
		return model.FromConfigWithLookup(cfg, func(key string) (string, bool) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			v, ok, _ := settings.Get(ctx, key)
			return v, ok
		})
	}, registry, executor, checkpoints)
	// task 工具（同步只读子 agent）：runner 同时满足 ChildExecutor + CheckpointCleaner。
	if err := registry.Register(subagent.TaskTool(registry, tasks, runner, runner)); err != nil {
		return err
	}
	rt := runtime.New(runtime.Deps{Tasks: tasks, Events: events, Approvals: approvals, Runner: runner}, cfg.WorkerConcurrency)

	if _, err := runtime.RecoverOrphans(ctx, tasks, events); err != nil {
		a.log.Warn("orphan recovery failed", "err", err)
	}
	a.skillLoader = skill.NewLoader(cfg.SkillsDir)
	_ = a.skillLoader.Refresh()
	a.manager = mcp.NewManager(mcp.NewStdioTransportFactory())
	tc, err := mcp.ReadToolConfig(ctx, settings)
	if err != nil {
		a.log.Warn("tools config read: mcp.servers decode failed; starting with empty server list", "err", err)
	}
	// Spec §4.3: MCP servers are only spawned + exposed when mcp.enabled.
	if tc.MCPEnabled {
		_ = a.manager.SpawnAll(ctx, tc.Servers)
		_ = registry.Replace("mcp__", mcp.MCPDefinitions(a.manager.Status(), a.manager))
	}
	if tc.SkillsEnabled {
		_ = registry.Remove("invoke_skill")
		if err := registry.Register(skill.InvokeSkill(a.skillLoader, 32*1024)); err != nil {
			return err
		}
		runner.SetSkillInstructions(skillListing(a.skillLoader))
	}
	provider := newToolConfigProvider(settings, a.manager, a.skillLoader, registry, runner)

	runs := service.NewRunService(cfg, tasks, events, approvals, rt, runner)
	sessions := service.NewSessionService(tasks)
	srv := httpapi.New(cfg, db, tasks, events, settings, runs, sessions,
		httpapi.WithToolConfigProvider(provider), httpapi.WithEventHub(hub))

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
	if a.manager != nil {
		_ = a.manager.StopAll()
	}
	if a.srv != nil {
		_ = a.srv.Shutdown(ctx)
	}
	if a.db != nil {
		return a.db.Close()
	}
	return nil
}
