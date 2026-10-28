package app

import (
	"context"

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
