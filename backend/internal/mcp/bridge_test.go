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
