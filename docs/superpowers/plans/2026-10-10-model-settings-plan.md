# Model Settings Page Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Global model-settings page backed by a persisted, write-only-key store that takes effect on newly created runs.

**Architecture:** New `server_settings` table + `SettingsRepo`; two REST endpoints with the API key never serialized back; the model factory closure in `app.go` reads overrides per call with env fallback; frontend settings dialog in `Header` wired to new API client methods.

**Tech Stack:** Go + SQLite (`modernc.org/sqlite`), net/http REST, React + shadcn Dialog, vitest.

Spec: `docs/superpowers/specs/2026-10-10-model-settings-design.md`.

---

## Chunk 1: Backend settings store + API

### Task 1: settings table + repo

**Files:**
- Modify: `backend/internal/store/schema.sql` (append `server_settings` table)
- Create: `backend/internal/store/settings_repo.go`
- Test: `backend/internal/store/settings_test.go`

- [ ] **Step 1: Append table to schema.sql**

```sql
CREATE TABLE IF NOT EXISTS server_settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);
```

- [ ] **Step 2: Write repo with failing test first**

```go
// settings_test.go
func TestSettingsRoundTrip(t *testing.T) {
    db, err := Open(":memory:")
    if err != nil { t.Fatal(err) }
    defer db.Close()
    r := NewSettingsRepo(db)
    ctx := context.Background()
    if err := r.Set(ctx, "model.name", "gpt-4o-mini"); err != nil { t.Fatal(err) }
    v, ok, err := r.Get(ctx, "model.name")
    if err != nil || !ok || v != "gpt-4o-mini" { t.Fatalf("got %q %v %v", v, ok, err) }
    if _, ok, _ := r.Get(ctx, "missing"); ok { t.Fatal("missing key must return ok=false") }
}
```

Run: `cd backend && go test ./internal/store/ -run TestSettingsRoundTrip -v`
Expected: FAIL with "undefined: NewSettingsRepo"

- [ ] **Step 3: Minimal repo implementation**

```go
package store

import (
    "context"
    "database/sql"
    "time"
)

type SettingsRepo struct{ db *sql.DB }

func NewSettingsRepo(db *sql.DB) *SettingsRepo { return &SettingsRepo{db: db} }

func (r *SettingsRepo) Set(ctx context.Context, key, value string) error {
    _, err := r.db.ExecContext(ctx, `INSERT INTO server_settings(key, value, updated_at)
        VALUES(?, ?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
        key, value, time.Now().UnixMilli())
    return err
}

func (r *SettingsRepo) Get(ctx context.Context, key string) (string, bool, error) {
    var v string
    err := r.db.QueryRowContext(ctx, `SELECT value FROM server_settings WHERE key = ?`, key).Scan(&v)
    if err == sql.ErrNoRows {
        return "", false, nil
    }
    if err != nil {
        return "", false, err
    }
    return v, true, nil
}
```

- [ ] **Step 4: Run test, expect PASS**

Run: `cd backend && go test ./internal/store/ -count=1 2>&1 | tail -n 3`
Expected: `ok`

- [ ] **Step 5: Commit**

```bash
git add backend/internal/store/schema.sql backend/internal/store/settings_repo.go backend/internal/store/settings_test.go
git commit -m "feat(backend): add server settings store"
```

### Task 2: settings REST endpoints

**Files:**
- Modify: `backend/internal/api/http/server.go` (routes + handlers; `Server` struct gains `settings *store.SettingsRepo`, update `New` signature and its caller in `internal/app/app.go`)
- Test: `backend/tests/integration/settings_test.go`

- [ ] **Step 1: Write failing integration test**

```go
package integration

import (
    "strings"
    "testing"

    "rivus-agent-backend/tests/testutil"
)

func TestModelSettingsRoundTrip(t *testing.T) {
    h := testutil.New(t)
    code, body := h.Do("GET", "/api/v1/settings/model", nil, nil)
    if code != 200 || !strings.Contains(string(body), "api_key_set") {
        t.Fatalf("GET: %d %s", code, body)
    }
    if strings.Contains(string(body), "sk-") {
        t.Fatal("api key must never be serialized")
    }
    code, body = h.Do("PUT", "/api/v1/settings/model",
        map[string]string{"base_url": "https://x.test/v1", "model": "m1", "api_key": "sk-test"}, nil)
    if code != 200 {
        t.Fatalf("PUT: %d %s", code, body)
    }
    _, body2 := h.Do("GET", "/api/v1/settings/model", nil, nil)
    for _, want := range []string{`"model":"m1"`, `"api_key_set":true`} {
        if !strings.Contains(string(body2), want) {
            t.Fatalf("missing %s in %s", want, body2)
        }
    }
    // empty api_key keeps the existing one
    h.Do("PUT", "/api/v1/settings/model", map[string]string{"model": "m2"}, nil)
    _, body3 := h.Do("GET", "/api/v1/settings/model", nil, nil)
    if !strings.Contains(string(body3), `"api_key_set":true`) {
        t.Fatalf("empty api_key must keep existing key: %s", body3)
    }
    // invalid provider rejected
    code, _ = h.Do("PUT", "/api/v1/settings/model", map[string]string{"provider": "nope"}, nil)
    if code != 400 {
        t.Fatalf("invalid provider must 400, got %d", code)
    }
}
```

Run: `cd backend && go test ./tests/integration/ -run TestModelSettingsRoundTrip -v`
Expected: FAIL (404, no route)

- [ ] **Step 2: Implement handlers (key never leaves the server)**

```go
// routes:
s.mux.HandleFunc("GET /api/v1/settings/model", s.handleGetModelSettings)
s.mux.HandleFunc("PUT /api/v1/settings/model", s.handleUpdateModelSettings)

// GET handler:
provider, _ := s.settings.Get(ctx, "model.provider") // fallback cfg.ModelProvider when missing
baseURL, _ := s.settings.Get(ctx, "model.base_url")   // fallback cfg.ModelBaseURL
name, _ := s.settings.Get(ctx, "model.name")          // fallback cfg.ModelName
_, keySet, _ := s.settings.Get(ctx, "model.api_key")  // fallback cfg.ModelAPIKey != ""
writeJSON(w, 200, map[string]any{"provider": ..., "base_url": ..., "model": ..., "api_key_set": keySet})

// PUT handler: decode {provider, base_url, model, api_key}; validate provider empty|openai_compat,
// base_url/model non-empty when provided; set only non-empty fields (api_key empty = keep).
// writeErr 400 on validation failure with code "bad_request".
```

- [ ] **Step 3: Wire repo through `New` + `app.go`, run test, expect PASS**

Run: `cd backend && go build ./... && go test ./tests/integration/ -run TestModelSettingsRoundTrip -count=1 -v 2>&1 | tail -n 4`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add backend/internal/api/http/server.go backend/internal/app/app.go backend/tests/integration/settings_test.go
git commit -m "feat(backend): add model settings REST endpoints"
```

### Task 3: per-run settings override in model factory

**Files:**
- Modify: `backend/internal/app/app.go` (factory closure reads `SettingsRepo` first, env config as fallback)
- Test: extend `backend/tests/integration/settings_test.go` with `TestNewRunUsesUpdatedSettings`

- [ ] **Step 1: Failing test — PUT new model name, create run, assert runner used it**

Script the harness model is fixed, so assert at the factory level instead: expose a helper `resolveModelConfig(cfg, settings)` in `internal/model/factory.go` and unit-test override precedence:

```go
// factory_test.go
func TestResolveModelConfigPrefersDB(t *testing.T) {
    db, _ := store.Open(":memory:")  // same pattern as store tests
    repo := store.NewSettingsRepo(db)
    repo.Set(context.Background(), "model.name", "db-model")
    got := resolveModelConfig(Config{ModelName: "env-model", ...}, repo)
    if got.Model != "db-model" { t.Fatalf("got %s", got.Model) }
}
```

Run: `cd backend && go test ./internal/model/ -run TestResolveModelConfigPrefersDB -v`
Expected: FAIL (undefined)

- [ ] **Step 2: Implement `resolveModelConfig` + use it in `app.go` closure**

- [ ] **Step 3: Run full backend suite, expect PASS**

Run: `cd backend && go test ./... -count=1 2>&1 | tail -n 8`
Expected: all `ok`

- [ ] **Step 4: Commit**

```bash
git add backend/internal/model/factory.go backend/internal/model/factory_test.go backend/internal/app/app.go
git commit -m "feat(backend): resolve model config per run with DB override"
```

---

## Chunk 2: Frontend settings dialog

### Task 4: dialog + wiring

**Files:**
- Modify: `frontend/src/lib/types.ts` (add `ModelSettings`), `frontend/src/lib/api.ts` (add methods), `frontend/src/components/Header.tsx` (gear button + dialog), create `frontend/src/components/SettingsDialog.tsx`
- Test: existing vitest suite must stay green; add `frontend/src/components/SettingsDialog.test.tsx` (renders fields, save calls PUT, empty key keeps existing)

- [ ] **Step 1: Types + API client**

```ts
// types.ts
export interface ModelSettings {
  provider: string
  base_url: string
  model: string
  api_key_set: boolean
  updated_at?: number
}
// api.ts
async getModelSettings(): Promise<ModelSettings> {
  return this.request<ModelSettings>('GET', '/api/v1/settings/model')
}
async updateModelSettings(patch: { provider?: string; base_url?: string; model?: string; api_key?: string }): Promise<void> {
  await this.request('PUT', '/api/v1/settings/model', patch)
}
```

- [ ] **Step 2: `SettingsDialog.tsx` (shadcn Dialog + Input + Button; provider Select fixed to `openai_compat`; api key as password input with set/unset badge; save disabled while busy; error text from `ApiError.message`)**

- [ ] **Step 3: Header gear button opens dialog; test**

Run: `cd frontend && node node_modules/typescript/bin/tsc --noEmit && npm test -- --run 2>&1 | tail -n 3`
Expected: tsc clean, all tests PASS

- [ ] **Step 4: Build + commit**

Run: `cd frontend && npx vite build 2>&1 | tail -n 1`
Expected: `✓ built in ...`

```bash
git add frontend/src/lib/types.ts frontend/src/lib/api.ts frontend/src/components/Header.tsx frontend/src/components/SettingsDialog.tsx frontend/src/components/SettingsDialog.test.tsx
git commit -m "feat(frontend): add model settings dialog"
```
