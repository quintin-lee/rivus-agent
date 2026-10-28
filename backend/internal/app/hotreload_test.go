package app

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	mcp "rivus-agent-backend/internal/mcp"
	"rivus-agent-backend/internal/store"
	"rivus-agent-backend/internal/tool"
)

// TestHotReloadAppliesMCPSection: editing the watched config.yaml applies the
// mcp section through to settings without a restart; an invalid section keeps
// the previous state.
func TestHotReloadAppliesMCPSection(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	db, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	settings := store.NewSettingsRepo(db)
	registry := tool.NewRegistry()
	a := &App{
		log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		manager: mcp.NewManager(mcp.NewFakeTransportFactory()),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Seed: mcp disabled.
	if err := os.WriteFile(cfgPath, []byte("mcp:\n  enabled: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	go a.watchConfigFile(ctx, cfgPath, settings, registry, 20*time.Millisecond)

	time.Sleep(60 * time.Millisecond) // let the watcher take its first stamp
	if err := os.WriteFile(cfgPath, []byte("mcp:\n  enabled: true\n  servers:\n    - name: echo\n      command: demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		v, ok, _ := settings.Get(ctx, mcp.KeyMCPEnabled)
		if ok && v == "true" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("hot reload did not apply mcp.enabled")
}

// TestHotReloadInvalidSpecKeepsPrevious: a yaml whose specs fail validation is
// ignored — settings keep the last good values.
func TestHotReloadInvalidSpecKeepsPrevious(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	db, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	settings := store.NewSettingsRepo(db)
	ctx := context.Background()
	if err := settings.Set(ctx, mcp.KeyMCPEnabled, "true"); err != nil {
		t.Fatal(err)
	}
	a := &App{
		log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		manager: mcp.NewManager(mcp.NewFakeTransportFactory()),
	}
	wctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := os.WriteFile(cfgPath, []byte("mcp:\n  enabled: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	go a.watchConfigFile(wctx, cfgPath, settings, tool.NewRegistry(), 20*time.Millisecond)
	time.Sleep(60 * time.Millisecond)
	if err := os.WriteFile(cfgPath, []byte("mcp:\n  enabled: true\n  servers:\n    - name: bad\n      type: http\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond) // several polls; invalid spec must be a no-op
	v, _, _ := settings.Get(ctx, mcp.KeyMCPEnabled)
	if v != "true" {
		t.Fatalf("previous mcp.enabled must be kept, got %q", v)
	}
	var servers string
	if v, _, _ := settings.Get(ctx, mcp.KeyMCP_servers); v != "" {
		servers = v
	}
	if servers != "" {
		t.Fatalf("invalid specs must not be persisted, got %q", servers)
	}
}
