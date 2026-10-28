package app

import (
	"context"
	"os"
	"time"

	"rivus-agent-backend/internal/config"
	mcp "rivus-agent-backend/internal/mcp"
	"rivus-agent-backend/internal/store"
	"rivus-agent-backend/internal/tool"
)

// applyMCPConfig writes the given MCP switch + server list through to the
// settings store and reconciles live connections + the shared registry's
// mcp__ family. Shared by the yaml write-through at startup and the file
// watcher (yaml 有 mcp 段 = yaml 权威；无段 = settings 权威).
func (a *App) applyMCPConfig(ctx context.Context, settings *store.SettingsRepo,
	registry *tool.Registry, enabled bool, servers []mcp.ServerSpec) error {
	if err := mcp.ValidateSpecs(servers); err != nil {
		return err
	}
	if err := settings.Set(ctx, mcp.KeyMCPEnabled, boolStr(enabled)); err != nil {
		return err
	}
	if err := settings.Set(ctx, mcp.KeyMCP_servers, marshalJSON(servers)); err != nil {
		return err
	}
	live := servers
	if !enabled {
		live = nil
	}
	if err := a.manager.Reconcile(ctx, live); err != nil {
		return err
	}
	return registry.Replace("mcp__", mcp.MCPDefinitions(a.manager.Status(), a.manager))
}

func mcpSpecsFromYaml(in []config.MCPServerYaml) []mcp.ServerSpec {
	out := make([]mcp.ServerSpec, 0, len(in))
	for _, s := range in {
		out = append(out, mcp.ServerSpec{Name: s.Name, Command: s.Command, Args: s.Args,
			Type: s.Type, URL: s.URL, Headers: s.Headers})
	}
	return out
}

// watchConfigFile polls cfgPath every interval; on mtime/size change it
// reloads the config and applies the mcp section (yaml-权威 semantics).
// Errors are logged and keep the previous state. Exits when ctx is cancelled.
func (a *App) watchConfigFile(ctx context.Context, cfgPath string,
	settings *store.SettingsRepo, registry *tool.Registry, interval time.Duration) {
	type stamp struct {
		mod  time.Time
		size int64
	}
	stat := func() (stamp, bool) {
		fi, err := os.Stat(cfgPath)
		if err != nil {
			return stamp{}, false
		}
		return stamp{fi.ModTime(), fi.Size()}, true
	}
	last, ok := stat()
	if !ok {
		return
	}
	tk := time.NewTicker(interval)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			cur, ok := stat()
			if !ok || (cur.mod.Equal(last.mod) && cur.size == last.size) {
				continue
			}
			last = cur
			cfg, err := config.Load(cfgPath)
			if err != nil {
				a.log.Warn("mcp hot reload: config load failed", "err", err)
				continue
			}
			if cfg.MCP == nil {
				continue // 无 mcp 段不动 settings（settings 权威）
			}
			if err := a.applyMCPConfig(ctx, settings, registry, cfg.MCP.Enabled, mcpSpecsFromYaml(cfg.MCP.Servers)); err != nil {
				a.log.Warn("mcp hot reload: apply failed", "err", err)
				continue
			}
			a.log.Info("mcp config reloaded", "servers", len(cfg.MCP.Servers), "enabled", cfg.MCP.Enabled)
		}
	}
}
