package config

import (
	"os"
	"testing"
)

func TestWorkspaceAndFetchConfig(t *testing.T) {
	c := Default()
	if c.WorkspaceDir != "./workspace" || c.KnowledgeDir != "./knowledge" {
		t.Fatalf("defaults wrong: %q %q", c.WorkspaceDir, c.KnowledgeDir)
	}
	t.Setenv("AGENT_WORKSPACE_DIR", "/tmp/ws")
	t.Setenv("AGENT_KNOWLEDGE_DIR", "/tmp/kn")
	t.Setenv("AGENT_HTTP_FETCH_ALLOWLIST", "example.com, api.openai.com")
	c2, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c2.WorkspaceDir != "/tmp/ws" || c2.KnowledgeDir != "/tmp/kn" {
		t.Fatalf("env override failed: %+v", c2)
	}
	if len(c2.HTTPFetchAllowlist) != 2 || c2.HTTPFetchAllowlist[1] != "api.openai.com" {
		t.Fatalf("allowlist split failed: %v", c2.HTTPFetchAllowlist)
	}
}

func TestRateLimitConfig(t *testing.T) {
	if c := Default(); c.RateLimitRPM != 0 {
		t.Fatalf("default rate_limit_rpm must be 0 (off), got %d", c.RateLimitRPM)
	}
	t.Setenv("AGENT_RATE_LIMIT_RPM", "120")
	c1, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c1.RateLimitRPM != 120 {
		t.Fatalf("env override failed: got %d, want 120", c1.RateLimitRPM)
	}
	t.Setenv("AGENT_RATE_LIMIT_RPM", "-5")
	c2, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c2.RateLimitRPM != 0 {
		t.Fatalf("negative rpm must be ignored (stay 0), got %d", c2.RateLimitRPM)
	}
	t.Setenv("AGENT_RATE_LIMIT_RPM", "abc")
	c3, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c3.RateLimitRPM != 0 {
		t.Fatalf("invalid rpm must be ignored (stay 0), got %d", c3.RateLimitRPM)
	}
}

func writeYaml(t *testing.T, content string) string {
	t.Helper()
	p := t.TempDir() + "/config.yaml"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMCPConfigYAML(t *testing.T) {
	yaml := "addr: ':0'\nmcp:\n  enabled: true\n  servers:\n    - name: fs\n      command: npx\n      args: ['-y', '@x/fs']\n    - name: remote\n      type: http\n      url: https://mcp.example.com\n      headers:\n        Authorization: Bearer k\n"
	cfg, err := Load(writeYaml(t, yaml))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MCP == nil || !cfg.MCP.Enabled || len(cfg.MCP.Servers) != 2 {
		t.Fatalf("mcp=%+v err=%v", cfg.MCP, err)
	}
	if cfg.MCP.Servers[0].Command != "npx" || len(cfg.MCP.Servers[0].Args) != 2 {
		t.Fatalf("stdio server=%+v", cfg.MCP.Servers[0])
	}
	if cfg.MCP.Servers[1].Type != "http" || cfg.MCP.Servers[1].Headers["Authorization"] != "Bearer k" {
		t.Fatalf("remote=%+v", cfg.MCP.Servers[1])
	}
	cfg2, err := Load(writeYaml(t, "addr: ':0'\n"))
	if err != nil || cfg2.MCP != nil {
		t.Fatalf("mcp=%+v err=%v", cfg2.MCP, err)
	}
}
