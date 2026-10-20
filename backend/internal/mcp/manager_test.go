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

// TestManagerMultiServerCoexist: one healthy + one crashed server can coexist;
// Status reports both (one "up", one "error").
func TestManagerMultiServerCoexist(t *testing.T) {
	f := NewFakeTransportFactory()
	f.ByServer["up"] = &FakeTransport{Tools: []string{"a"}, CallResult: "ok"}
	f.ByServer["dead"] = &FakeTransport{Crash: true}
	m := NewManager(f)
	if err := m.SpawnAll(context.Background(), []ServerSpec{
		{Name: "up", Command: "up-mcp"},
		{Name: "dead", Command: "dead-mcp"},
	}); err != nil {
		t.Fatal(err)
	}
	st := m.Status()
	if len(st) != 2 {
		t.Fatalf("Status len = %d, want 2: %+v", len(st), st)
	}
	var ups, errs int
	for _, e := range st {
		switch e.Status {
		case "up":
			ups++
		case "error":
			errs++
		default:
			t.Fatalf("unexpected status %q for %s", e.Status, e.Name)
		}
	}
	if ups != 1 || errs != 1 {
		t.Fatalf("want 1 up + 1 error, got %+v", st)
	}
}

// TestSpawnAllIdempotent: spawning the same already-connected server again is a
// no-op (no error, exactly one entry).
func TestSpawnAllIdempotent(t *testing.T) {
	f := NewFakeTransportFactory()
	f.ByServer["fs"] = &FakeTransport{Tools: []string{"read"}, CallResult: "ok"}
	m := NewManager(f)
	specs := []ServerSpec{{Name: "fs", Command: "npx"}}
	if err := m.SpawnAll(context.Background(), specs); err != nil {
		t.Fatal(err)
	}
	if err := m.SpawnAll(context.Background(), specs); err != nil {
		t.Fatalf("second SpawnAll must be a no-op, got %v", err)
	}
	st := m.Status()
	if len(st) != 1 || st[0].Status != "up" {
		t.Fatalf("after double SpawnAll Status = %+v", st)
	}
}

// TestReconcileKeepAndDrop: reconciling to a smaller set keeps the retained
// client untouched and removes the dropped server from Status.
func TestReconcileKeepAndDrop(t *testing.T) {
	f := NewFakeTransportFactory()
	f.ByServer["a"] = &FakeTransport{Tools: []string{"read"}, CallResult: "ok"}
	f.ByServer["b"] = &FakeTransport{Tools: []string{"write"}, CallResult: "ok"}
	m := NewManager(f)
	if err := m.SpawnAll(context.Background(), []ServerSpec{
		{Name: "a", Command: "a-mcp"},
		{Name: "b", Command: "b-mcp"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.Reconcile(context.Background(), []ServerSpec{{Name: "a", Command: "a-mcp"}}); err != nil {
		t.Fatal(err)
	}
	st := m.Status()
	if len(st) != 1 || st[0].Name != "a" || st[0].Status != "up" {
		t.Fatalf("after Reconcile Status = %+v, want only a up", st)
	}
	if out, err := m.Call(context.Background(), "a", "read", nil); err != nil || out != "ok" {
		t.Fatalf("kept server still serviceable: out=%q err=%v", out, err)
	}
}

// TestReconcileStaleErrorCleanup: a server whose connect failed (error status,
// no live client) is dropped from Status when reconciled away from the spec set.
func TestReconcileStaleErrorCleanup(t *testing.T) {
	f := NewFakeTransportFactory()
	f.ByServer["dead"] = &FakeTransport{Crash: true}
	f.ByServer["ok"] = &FakeTransport{Tools: []string{"read"}, CallResult: "ok"}
	m := NewManager(f)
	if err := m.SpawnAll(context.Background(), []ServerSpec{
		{Name: "dead", Command: "dead-mcp"},
		{Name: "ok", Command: "ok-mcp"},
	}); err != nil {
		t.Fatal(err)
	}
	if st := m.Status(); len(st) != 2 {
		t.Fatalf("pre-reconcile Status = %+v, want 2", st)
	}
	if err := m.Reconcile(context.Background(), []ServerSpec{{Name: "ok", Command: "ok-mcp"}}); err != nil {
		t.Fatal(err)
	}
	st := m.Status()
	if len(st) != 1 || st[0].Name != "ok" || st[0].Status != "up" {
		t.Fatalf("stale error entry not cleaned: %+v", st)
	}
}
