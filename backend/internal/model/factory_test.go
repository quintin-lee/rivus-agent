package model

import (
	"context"
	"strings"
	"testing"

	"rivus-agent-backend/internal/config"
	"rivus-agent-backend/internal/store"
)

func testConfig() config.Config {
	cfg := config.Default()
	cfg.ModelProvider = "openai_compat"
	cfg.ModelName = "env-model"
	cfg.ModelBaseURL = "https://env.test/v1"
	cfg.ModelAPIKey = "env-key"
	return cfg
}

func TestFromConfigWithLookupPrefersDB(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := store.NewSettingsRepo(db)
	ctx := context.Background()
	if err := repo.Set(ctx, "model.provider", "nope"); err != nil {
		t.Fatal(err)
	}
	_, err = FromConfigWithLookup(testConfig(), func(key string) (string, bool) {
		v, ok, _ := repo.Get(ctx, key)
		return v, ok
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported provider") {
		t.Fatalf("db override must win, got %v", err)
	}
}

func TestFromConfigWithLookupFallsBackToEnv(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := store.NewSettingsRepo(db)
	ctx := context.Background()
	if _, err := FromConfigWithLookup(testConfig(), func(key string) (string, bool) {
		v, ok, _ := repo.Get(ctx, key)
		return v, ok
	}); err != nil {
		t.Fatalf("empty db must fall back to env: %v", err)
	}
}
