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

	einomodel "github.com/cloudwego/eino/components/model"
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

	if st, err := store.Stats(db); err == nil {
		a.log.Info("db stats",
			"page_count", st["page_count"], "page_size", st["page_size"],
			"freelist_count", st["freelist_count"], "file_bytes", st["file_bytes"],
			"wal_bytes", st["wal_bytes"])
	} else {
		a.log.Warn("db stats unavailable", "err", err)
	}

	approvals := store.NewApprovalRepo(db)
	checkpoints := store.NewCheckpointStore(db)

	registry := tool.NewRegistry()
	builtin.SetDirs(a.cfg.WorkspaceDir, a.cfg.KnowledgeDir)
	builtin.SetHTTPAllowlist(a.cfg.HTTPFetchAllowlist)
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
	// tasks 工具（批量并行委派，默认并发 4 / 上限 8 / 批上限 5）。
	if err := registry.Register(subagent.TasksTool(registry, tasks, runner, runner)); err != nil {
		return err
	}
	// 记忆工具（会话级持久记忆，LRU 100 条/会话）。
	memories := store.NewMemoryRepo(db)
	for _, d := range builtin.MemoryTools(memories) {
		if err := registry.Register(d); err != nil {
			return err
		}
	}
	rt := runtime.New(runtime.Deps{
		Tasks: tasks, Events: events, Approvals: approvals, Runner: runner, Log: a.log, Memories: memories,
	}, cfg.WorkerConcurrency)

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
		a.manager.StartReconciler(ctx, 30*time.Second) // 故障服务器每 30s 自动重连
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
		httpapi.WithToolConfigProvider(provider), httpapi.WithEventHub(hub), httpapi.WithLogger(a.log))

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
	var firstErr error
	if a.manager != nil {
		if err := a.manager.StopAll(); err != nil {
			a.log.Warn("mcp stop failed", "err", err)
		}
	}
	if a.srv != nil {
		if err := a.srv.Shutdown(ctx); err != nil {
			a.log.Warn("http shutdown failed", "err", err)
			firstErr = err
		}
	}
	if a.db != nil {
		if err := store.Checkpoint(a.db); err != nil {
			a.log.Warn("db checkpoint failed", "err", err)
		}
		if err := a.db.Close(); err != nil {
			a.log.Warn("db close failed", "err", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}
