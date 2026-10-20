package mcp

import (
	"context"
	"testing"

	"rivus-agent-backend/internal/tool"
)

func TestMCPDefinitions(t *testing.T) {
	f := NewFakeTransportFactory()
	f.ByServer["fs"] = &FakeTransport{Tools: []string{"read"}, CallResult: "data"}
	m := NewManager(f)
	_ = m.SpawnAll(context.Background(), []ServerSpec{{Name: "fs", Command: "npx"}})
	// fs fake (pre-seeded above) returns one tool "read"
	defs := MCPDefinitions(m.Status(), m)
	if len(defs) != 1 {
		t.Fatalf("want 1 def, got %d", len(defs))
	}
	d := defs[0]
	if d.Name != "mcp__fs__read" {
		t.Fatalf("name = %q", d.Name)
	}
	if d.Risk != tool.RiskExternal || d.Approval != tool.ApprovalAlways {
		t.Fatalf("want external/always, got %s/%s", d.Risk, d.Approval)
	}
	if len(d.RequiredScopes) != 0 {
		t.Fatalf("RequiredScopes must be empty (else CheckPolicy hard-denies), got %v", d.RequiredScopes)
	}
	if d.TimeoutMs != 30000 {
		t.Fatalf("TimeoutMs = %d, want 30000 (default)", d.TimeoutMs)
	}
}

// TestMCPDefinitionsSkipsNonUp pins the §4.3 dead-server guard:
// only StatusEntry.Status == "up" servers expose tools. A non-up entry that
// nonetheless carries cached Tools (e.g. a stale snapshot of a now-crashed
// server) must NOT leak those tools into the model's list.
func TestMCPDefinitionsSkipsNonUp(t *testing.T) {
	m := NewManager(NewFakeTransportFactory())
	status := []StatusEntry{
		{Name: "fs", Command: "npx", Status: "up", Tools: []Tool{{Name: "read"}}},
		// Simulate a stale snapshot: an error server that still carries cached tools.
		{Name: "git", Command: "git-mcp", Status: "error", Tools: []Tool{{Name: "status"}}},
	}
	defs := MCPDefinitions(status, m)
	if len(defs) != 1 {
		t.Fatalf("want only the up-server tool, got %d defs: %+v", len(defs), defs)
	}
	if defs[0].Name != "mcp__fs__read" {
		t.Fatalf("name = %q, want mcp__fs__read", defs[0].Name)
	}
}
