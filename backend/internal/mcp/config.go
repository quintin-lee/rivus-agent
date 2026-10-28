package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"

	"rivus-agent-backend/internal/store"
)

// Settings keys (global, stored as raw strings in server_settings).
const (
	KeySkillsEnabled = "skills.enabled"
	KeyMCPEnabled    = "mcp.enabled"
	KeyMCP_servers   = "mcp.servers"
)

// ServerSpec is one configured MCP server: a stdio child process or a remote
// HTTP endpoint.
type ServerSpec struct {
	Name    string            `json:"name"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Type    string            `json:"type,omitempty"` // "" | "stdio" | "http"
	URL     string            `json:"url,omitempty"`  // http(s) endpoint for type=http
	Headers map[string]string `json:"headers,omitempty"`
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
		return nil, fmt.Errorf("mcp.servers: %w", err)
	}
	if err := ValidateSpecs(out); err != nil {
		return nil, fmt.Errorf("mcp.servers: %w", err)
	}
	return out, nil
}

// ValidateSpecs checks the name/command/url rules for a spec list.
func ValidateSpecs(specs []ServerSpec) error {
	for _, s := range specs {
		if !ValidServerName(s.Name) {
			return fmt.Errorf("server %q: invalid name", s.Name)
		}
		switch s.Type {
		case "", "stdio":
			if s.Command == "" {
				return fmt.Errorf("server %q: empty command", s.Name)
			}
		case "http":
			u, err := url.Parse(s.URL)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return fmt.Errorf("server %q: invalid http url %q", s.Name, s.URL)
			}
		default:
			return fmt.Errorf("server %q: unknown type %q", s.Name, s.Type)
		}
	}
	return nil
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
