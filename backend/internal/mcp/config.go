package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	"rivus-agent-backend/internal/store"
)

// Settings keys (global, stored as raw strings in server_settings).
const (
	KeySkillsEnabled = "skills.enabled"
	KeyMCPEnabled    = "mcp.enabled"
	KeyMCP_servers   = "mcp.servers"
)

// ServerSpec is one configured MCP server (stdio).
type ServerSpec struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

// ToolConfig is the global tool configuration surfaced by /settings/tools.
type ToolConfig struct {
	SkillsEnabled bool
	MCPEnabled    bool
	Servers       []ServerSpec
}

var nameRe = regexp.MustCompile(`^[a-z0-9_-]+$`)

// ValidServerName enforces the §4.4 name rule.
func ValidServerName(s string) bool {
	return s != "" && nameRe.MatchString(s)
}

// ServersFromJSON decodes the mcp.servers setting value.
func ServersFromJSON(v string) ([]ServerSpec, error) {
	if v == "" {
		return nil, nil
	}
	var out []ServerSpec
	if err := json.Unmarshal([]byte(v), &out); err != nil {
		return nil, err
	}
	for i := range out {
		if !ValidServerName(out[i].Name) || out[i].Command == "" {
			return nil, fmt.Errorf("server %q: invalid name or empty command", out[i].Name)
		}
	}
	return out, nil
}

// ReadToolConfig reads all three keys from the settings store.
func ReadToolConfig(ctx context.Context, s *store.SettingsRepo) (ToolConfig, error) {
	var out ToolConfig
	if v, ok, err := s.Get(ctx, KeySkillsEnabled); err == nil && ok {
		out.SkillsEnabled = v == "true"
	}
	if v, ok, err := s.Get(ctx, KeyMCPEnabled); err == nil && ok {
		out.MCPEnabled = v == "true"
	}
	servers, err := ServersFromJSON(getStr(ctx, s, KeyMCP_servers))
	if err != nil {
		return out, err
	}
	out.Servers = servers
	return out, nil
}

// getStr reads a settings key; a read error is treated as "absent" (empty)
// on purpose — a missing mcp.servers key means "no MCP servers", not a failure.
func getStr(ctx context.Context, s *store.SettingsRepo, key string) string {
	v, _, _ := s.Get(ctx, key)
	return v
}
