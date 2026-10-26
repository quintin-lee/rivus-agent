package config

import "testing"

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
