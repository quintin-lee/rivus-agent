package mcp

import (
	"context"
	"strings"
	"testing"
)

func TestManagerLifecycle(t *testing.T) {
	f := NewFakeTransportFactory()
	f.ByServer["fs"] = &FakeTransport{Tools: []string{"read"}, CallResult: "data"}
	m := NewManager(f)

	// SpawnAll: one up server
	if err := m.SpawnAll(context.Background(), []ServerSpec{{Name: "fs", Command: "npx"}}); err != nil {
		t.Fatal(err)
	}
	if st := m.Status(); len(st) != 1 || st[0].Status != "up" || len(st[0].Tools) != 1 {
		t.Fatalf("Status = %+v", st)
	}

	// Reconcile: remove fs, add git (crash mode)
	f.ByServer["git"] = &FakeTransport{Crash: true}
	if err := m.Reconcile(context.Background(), []ServerSpec{{Name: "git", Command: "git-mcp"}}); err != nil {
		t.Fatal(err)
	}
	st := m.Status()
	if len(st) != 1 || st[0].Name != "git" {
		t.Fatalf("after Reconcile Status = %+v", st)
	}
	if st[0].Status != "error" { // crashed server -> error, others continue
		t.Fatalf("git status = %q, want error", st[0].Status)
	}

	// Call on a crashed server -> "disconnected"
	if _, err := m.Call(context.Background(), "git", "status", nil); err == nil || !strings.Contains(err.Error(), "disconnected") {
		t.Fatalf("want disconnected error, got %v", err)
	}

	// StopAll clears live connections
	if err := m.StopAll(); err != nil {
		t.Fatal(err)
	}
	if len(m.Status()) != 0 {
		t.Fatalf("StopAll must clear status, got %+v", m.Status())
	}
}
