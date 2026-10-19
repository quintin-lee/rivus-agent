# Extensible Agent Tooling (Skill + MCP + Global Tool Config) Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the rivus-agent a general-purpose agent that can call **MCP** tools and **skill** tools, driven by a **global tool-config** surface, all bridged into the one `tool.Registry` the runner already reads.

**Architecture:** New `internal/mcp` package (stdio `Transport` + `client` + `Manager` that spawns servers at start, keeps them alive, and mutates the shared `*tool.Registry` in place on config change). New `invoke_skill` tool backed by the existing `skill.Loader`. A `ToolConfigProvider` (implemented in `internal/app`, injected into `httpapi.New`) exposes `GET/PUT /api/v1/settings/tools` so the frontend Settings "Tools" panel can toggle skills, list MCP servers + discovered tools, and trigger reconcile. Two bounded `EinoRunner` deltas: guarded skill-name instruction augmentation and an `EvtToolFailed` emission. No new tool execution path.

**Tech Stack:** Go 1.27+ (CGO-free), CloudWeGo Eino ADK, `modernc.org/sqlite`, existing `tool.Registry`/`Executor`/`CheckPolicy`/approval pipeline; React + Vite frontend.

**Spec:** `docs/superpowers/specs/2026-10-15-extensible-tooling-design.md`

---

## File Structure

New files:
- `backend/internal/tool/registry_mutate.go` — `Remove`, `RemoveByPrefix`, `Replace` on `*Registry`
- `backend/internal/mcp/transport.go` — `Transport` interface + `stdioTransport`
- `backend/internal/mcp/client.go` — one-server JSON-RPC client (`initialize`/`tools/list`/`tools/call`)
- `backend/internal/mcp/manager.go` — `Manager` (`SpawnAll`/`Reconcile`/`StopAll`/`Status`/`Call`)
- `backend/internal/mcp/config.go` — `ServerSpec`, `ToolConfig`, settings key constants + encode/decode
- `backend/internal/skill/skill_tool.go` — `InvokeSkill` `tool.Definition`
- `backend/internal/app/toolconfig.go` — `ToolConfigProvider` impl (App owns SettingsRepo + Manager + Loader)
- `backend/tests/integration/subagent`-style `toolconfig_test.go` — integration tests
- `frontend/src/components/ToolsPanel.tsx` + a settings store slice

Modified files:
- `backend/internal/tool/registry.go` — (only if needed; prefer new file `registry_mutate.go`)
- `backend/internal/runtime/runner_eino.go` + `runner_child.go` — instruction setter + `EvtToolFailed`
- `backend/internal/app/app.go` — construct Manager + wire Loader + register tools + Stop→StopAll
- `backend/internal/api/http/server.go` — `ToolConfigProvider` param + `/settings/tools` routes
- `backend/tests/testutil/harness.go` — register `invoke_skill` + a fake MCP server for integration tests
- `frontend/src/components/SettingsDialog.tsx` + `frontend/src/lib/*` — Tools panel + API

Sequenced chunks (each compiles + tests green on its own):

---

## Chunk 1: `tool.Registry` mutation + tool-config schema

### Task 1: `Registry.Remove` / `RemoveByPrefix` / `Replace`

**Files:**
- Create: `backend/internal/tool/registry_mutate.go`
- Test: `backend/internal/tool/registry_mutate_test.go`

The current `Registry` (see `registry.go:48-93`) only has `Register` (rejects duplicates), `Get`, `List`. Reconcile needs in-place removal/re-add of `mcp__`/`invoke_skill` entries without re-threading the `*Registry` held by `EinoRunner`.

- [ ] **Step 1: Write the failing test**

`backend/internal/tool/registry_mutate_test.go`:
```go
package tool

import (
	"context"
	"encoding/json"
	"testing"
)

func TestRegistryMutate(t *testing.T) {
	r := NewRegistry()
	reg := func(name string) {
		h := func(ctx context.Context, args json.RawMessage) (string, error) { return "", nil }
		if err := r.Register(Definition{Name: name, Handler: h}); err != nil {
			t.Fatal(err)
		}
	}
	reg("builtin")
	reg("mcp__fs__read")
	reg("mcp__fs__write")
	reg("mcp__git__status")

	// exact-name remove
	if !r.Remove("mcp__git__status") {
		t.Fatal("Remove(mcp__git__status) should report present")
	}
	if _, ok := r.Get("mcp__git__status"); ok {
		t.Fatal("mcp__git__status should be gone")
	}
	if r.Remove("mcp__git__status") {
		t.Fatal("second Remove must report not-present")
	}

	// prefix remove returns count
	if n := r.RemoveByPrefix("mcp__"); n != 2 {
		t.Fatalf("RemoveByPrefix(mcp__) = %d, want 2", n)
	}

	// Replace clears a prefix then re-adds (through Register's normalization)
	if err := r.Replace("mcp__", []Definition{
		{Name: "mcp__fs__read", Handler: func(ctx context.Context, args json.RawMessage) (string, error) { return "", nil }},
	}); err != nil {
		t.Fatal(err)
	}
	if got := len(r.List()); got != 2 { // builtin + mcp__fs__read
		t.Fatalf("List len = %d, want 2", got)
	}
	if _, ok := r.Get("mcp__fs__write"); ok {
		t.Fatal("stale mcp__fs__write must be gone after Replace")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/tool/ -run TestRegistryMutate -v`
Expected: FAIL — `undefined: r.Remove` / `r.RemoveByPrefix` / `r.Replace`.

- [ ] **Step 3: Implement**

`backend/internal/tool/registry_mutate.go`:
```go
package tool

// Remove deletes a tool by exact name. Returns whether it was present.
func (r *Registry) Remove(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.tools[name]
	delete(r.tools, name)
	return ok
}

// RemoveByPrefix deletes every tool whose name starts with prefix. Returns the count.
func (r *Registry) RemoveByPrefix(prefix string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for k := range r.tools {
		if hasPrefix(k, prefix) {
			delete(r.tools, k)
			n++
		}
	}
	return n
}

// Replace clears all tools whose name starts with prefix, then re-adds defs
// through Register (so TimeoutMs/MaxResultBytes defaults + dup checks stay
// single-sourced). Returns the first registration error.
func (r *Registry) Replace(prefix string, defs []Definition) error {
	r.RemoveByPrefix(prefix)
	for i := range defs {
		if err := r.Register(defs[i]); err != nil {
			return err
		}
	}
	return nil
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/tool/ -run TestRegistryMutate -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/tool/registry_mutate.go backend/internal/tool/registry_mutate_test.go
git commit -m "feat(tool): in-place Registry Remove/RemoveByPrefix/Replace for live tool reconcile"
```

### Task 2: tool-config schema + settings keys

**Files:**
- Create: `backend/internal/mcp/config.go`
- Test: `backend/internal/mcp/config_test.go`

Defines the settings keys (§4.4) and the typed config they decode from the `server_settings` table (global, key/value — no migration).

- [ ] **Step 1: Write the failing test**

`backend/internal/mcp/config_test.go`:
```go
package mcp

import (
	"encoding/json"
	"testing"
)

func TestToolConfigCodec(t *testing.T) {
	in := ToolConfig{
		SkillsEnabled: true,
		MCPEnabled:    true,
		Servers: []ServerSpec{
			{Name: "fs", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"}},
		},
	}
	b, _ := json.Marshal(in.Servers)
	var got []ServerSpec
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "fs" {
		t.Fatalf("codec round-trip wrong: %+v", got)
	}
}

func TestServerNameValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"fs", true}, {"my_srv-1", true}, {"Bad Name", false}, {"a/b", false}, {"", false},
	} {
		if got := ValidServerName(tc.name); got != tc.want {
			t.Fatalf("ValidServerName(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/mcp/ -run 'TestToolConfigCodec|TestServerNameValidation' -v`
Expected: FAIL — `undefined: ToolConfig` / `ValidServerName`.

- [ ] **Step 3: Implement**

`backend/internal/mcp/config.go`:
```go
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	"rivus-agent-backend/internal/store"
)

// Settings keys (global, stored as raw strings in server_settings).
const (
	KeySkillsEnabled = "skills.enabled"
	KeyMCPEnabled    = "mcp.enabled"
	KeyMCP_servers   = "mcp.servers"
)

// ServerSpec is one configured MCP server (stdio).
type ServerSpec struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
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
		return nil, err
	}
	for i := range out {
		if !ValidServerName(out[i].Name) || out[i].Command == "" {
			return nil, fmt.Errorf("server %q: invalid name or empty command", out[i].Name)
		}
	}
	return out, nil
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

func getStr(ctx context.Context, s *store.SettingsRepo, key string) string {
	v, _, _ := s.Get(ctx, key)
	return v
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/mcp/ -run 'TestToolConfigCodec|TestServerNameValidation' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/mcp/config.go backend/internal/mcp/config_test.go
git commit -m "feat(mcp): tool-config schema + settings keys (skills.enabled/mcp.enabled/mcp.servers)"
```

---

## Chunk 2: `internal/mcp` (Transport + client + Manager) + registry bridge

### Task 3: stdio `Transport`

**Files:**
- Create: `backend/internal/mcp/transport.go`
- Test: `backend/internal/mcp/transport_test.go`

One JSON-RPC message per line over a stdio child process in its own process group.

- [ ] **Step 1: Write the failing test** — a fake in-process "MCP server" (a Go function serving on a pipe) is the realistic seam, but `stdioTransport` shells out. Test the interface contract minimally with a `/bin/cat`-style echo process:
```go
package mcp

import (
	"context"
	"testing"
)

func TestStdioTransportLifecycle(t *testing.T) {
	tt, err := NewStdioTransport(context.Background(), "/bin/echo", []string{""})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = tt.Stop() }()
	if err := tt.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	frame, err := tt.ReadFrame()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(frame) != "hello " { // /bin/echo appends a space? assert round-trip of what was written
		t.Fatalf("frame = %q", frame)
	}
}
```
(If `/bin/echo` behavior is flaky on CI, use `python3 -c "import sys; print(sys.stdin.readline().rstrip())"` or the in-pipe fake from Task 5. The test's point: Start→Write→ReadFrame→Stop round-trips a line and Stop kills the child.)

- [ ] **Step 2: Run to verify fail** — `cd backend && go test ./internal/mcp/ -run TestStdioTransportLifecycle -v` → `undefined: NewStdioTransport`.
- [ ] **Step 3: Implement** — `Transport` interface `{ Start(ctx) error; Write([]byte) error; ReadFrame() ([]byte, error); Stop() error }` + `stdioTransport` (`os/exec` with `SysProcAttr{Setpgid: true}`, `io.Pipe` for stdin/stdout, `cmd.ProcessGroup` kill on Stop, line-buffered reads).
- [ ] **Step 4: Run to verify pass** — same command → PASS.
- [ ] **Step 5: Commit** — `git commit -m "feat(mcp): stdio Transport (child process group, line framing)"`.

### Task 4: JSON-RPC `client`

**Files:**
- Create: `backend/internal/mcp/client.go`
- Test: `backend/internal/mcp/client_test.go`

Speaks `initialize` → `tools/list` → `tools/call` over a `Transport`; maps MCP content blocks to `(string, error)`.

- [ ] **Step 1: Write failing test** — use the in-pipe fake MCP server from Task 5 (a scripted JSON-RPC responder): canned `initialize`→result, `tools/list`→one tool, `tools/call`→a text content block. Assert `ListTools` returns the tool and `Call` returns its text; an `isError` result maps to a Go error.
- [ ] **Step 2: Run fail** — `undefined: NewClient` / `Client.ListTools`.
- [ ] **Step 3: Implement** — `client.go`: request-id counter, one-message-per-line JSON-RPC writer/reader with a 5s deadline on `initialize`/`tools/list`; `tools/call` maps `result.content[]` text blocks (joined) → string, `result.isError` → error.
- [ ] **Step 4: Run pass**.
- [ ] **Step 5: Commit** — `git commit -m "feat(mcp): JSON-RPC client (initialize/tools list/call, block→string mapping)"`.

### Task 5: in-process fake MCP stdio server (test seam)

**Files:**
- Create: `backend/internal/mcp/fake_test.go` (test-only)

A fake that speaks just enough MCP stdio to drive `client`/`manager` tests. This is the seam §7 relies on for crash + reconcile tests.

- [ ] **Step 1: Implement the fake** — a small stdio JSON-RPC responder (canned initialize/tools_list/call; a "crash" mode that closes its pipes; a "down" mode that never answers). Keep it in `mcp` (test-only, `_test.go`) so client/manager tests can construct it.
- [ ] **Step 2: Add a smoke test** that the fake answers `initialize`+`tools/list`.
- [ ] **Step 3: Run** — `go test ./internal/mcp/ -run TestFakeServer -v` → PASS.
- [ ] **Step 4: Commit** — `git commit -m "test(mcp): in-process fake stdio MCP server (up/crash/down modes)"`.

### Task 6: `Manager` (SpawnAll / Reconcile / StopAll / Status / Call)

**Files:**
- Create: `backend/internal/mcp/manager.go`
- Test: `backend/internal/mcp/manager_test.go`

- [ ] **Step 1: Write failing test** — with the fake server:
  - `SpawnAll` of one server → `Status()` has one entry `status="up"` with the cached `tools`.
  - `Reconcile` removing that server → entry gone; adding a second → new entry appears.
  - `Call` against a crashed server returns an `error` containing `"disconnected"`.
  - `StopAll` → all statuses cleared/stopped.
- [ ] **Step 2: Run fail** — `undefined: NewManager`.
- [ ] **Step 3: Implement** — `Manager` owns `map[string]*client` (or connection) + a `Status()` slice matching §4.6 shape `{name, command, status:"up"|"down"|"error", err, tools:[{name,description}]}`. `SpawnAll(cfg)` spawns each; a failed `initialize` marks `status="error"` (logged + skipped, others continue). `Reconcile(cfg)` diffs the set: stop removed, spawn added, return the fresh `[]tool.Definition` for the `mcp__` family. `Call(server, tool, args, timeout)` → `tools/call`; a dead connection returns `"mcp server <name> disconnected"`.
- [ ] **Step 4: Run pass**.
- [ ] **Step 5: Commit** — `git commit -m "feat(mcp): Manager (spawn/reconcile/status/call, keep-alive)"`.

### Task 7: MCP → `tool.Definition` bridge

**Files:**
- Create: `backend/internal/mcp/bridge.go`
- Test: `backend/internal/mcp/bridge_test.go`

- [ ] **Step 1: Write failing test** — given a `Manager.Status()` with tools, `MCPDefinitions` returns one `tool.Definition` per tool named `mcp__<server>__<tool>`, `Risk=external, Approval=always`, **`RequiredScopes` empty**, and a `Handler` that delegates to `manager.Call` with the tool's default `TimeoutMs` (30000).
- [ ] **Step 2: Run fail** — `undefined: MCPDefinitions`.
- [ ] **Step 3: Implement** — `MCPDefinitions(st []StatusEntry, mgr *Manager) []tool.Definition` (build the defs; the handler marshals its `json.RawMessage` to `mgr.Call`). Enforce the §4.3 invariant: `RequiredScopes` stays empty.
- [ ] **Step 4: Run pass**.
- [ ] **Step 5: Commit** — `git commit -m "feat(mcp): bridge MCP tools to tool.Definition (external/always, empty scopes, mangled names)"`.

---

## Chunk 3: `invoke_skill` + runner deltas

### Task 8: `invoke_skill` tool

**Files:**
- Create: `backend/internal/skill/skill_tool.go`
- Test: `backend/internal/skill/skill_tool_test.go`

- [ ] **Step 1: Write failing test** — a `Loader` pointed at a fixture `SKILL.md` (name `demo`, description in frontmatter-ish `description:` line). `InvokeSkill(loader, maxBytes)`'s handler returns the body for `{"name":"demo"}`; returns an error for an unknown name. `Risk=read, Approval=never, RequiredScopes` empty.
- [ ] **Step 2: Run fail** — `undefined: InvokeSkill`.
- [ ] **Step 3: Implement** — `skill_tool.go`:
```go
package skill

import (
	"context"
	"encoding/json"
	"fmt"

	"rivus-agent-backend/internal/tool"
)

// InvokeSkill builds the `invoke_skill` tool.Definition backed by the Loader.
// Risk=read, Approval=never, RequiredScopes empty. Handler returns the SKILL.md
// body (truncated to maxBytes) as the tool result for a known skill name.
func InvokeSkill(l *Loader, maxBytes int) tool.Definition {
	return tool.Definition{
		Name:        "invoke_skill",
		Description: "Load a skill's instructions as context. Args: {\"name\":\"<skill>\"}.",
		Version:     "v1",
		InputSchema: `{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`,
		Risk:        tool.RiskRead,
		Approval:    tool.ApprovalNever,
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct{ Name string `json:"name"` }
			if err := json.Unmarshal(args, &in); err != nil || in.Name == "" {
				return "", fmt.Errorf("invoke_skill: invalid args")
			}
			body, ok := l.Get(in.Name)
			if !ok {
				return "", fmt.Errorf("unknown or unavailable skill %q", in.Name)
			}
			if maxBytes > 0 && len(body) > maxBytes {
				body = body[:maxBytes]
			}
			return body, nil
		},
	}
}
```
- [ ] **Step 4: Run pass** — `go test ./internal/skill/ -run TestInvokeSkill -v`.
- [ ] **Step 5: Commit** — `git commit -m "feat(skill): invoke_skill tool backed by Loader (read/never)"`.

### Task 9: runner delta (a) — guarded skill-name instruction augmentation

**Files:**
- Modify: `backend/internal/runtime/runner_eino.go` (add setter)
- Modify: `backend/internal/runtime/runner_child.go:104` (consume augmented instruction)
- Test: `backend/internal/runtime/instruction_test.go`

`instruction` is read in `buildAgentFiltered` without the runner mutex (`runner_child.go:105`), so the write must be guarded.

- [ ] **Step 1: Write failing test** — build a runner, `SetSkillInstructions("demo: does X")`, assert `buildAgent`'s assembled instruction contains `demo`. Also assert concurrent `SetSkillInstructions` + build doesn't race (`go test -race`).
- [ ] **Step 2: Run fail** — `undefined: r.SetSkillInstructions`.
- [ ] **Step 3: Implement** — add to `EinoRunner`:
```go
func (r *EinoRunner) SetSkillInstructions(listening string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if listening != "" {
		r.instruction = r.instruction + "\n\n可用技能：\n" + listening
	}
}
```
and in `buildAgentFiltered`/`buildAgent`, read `r.instruction` under `r.mu.RLock()`. (The runner's existing `mu` already guards `needApprovalByRun`; reuse it.)
- [ ] **Step 4: Run pass** — `go test -race ./internal/runtime/ -run TestInstruction -v`.
- [ ] **Step 5: Commit** — `git commit -m "feat(runtime): guarded skill-name instruction augmentation (delta a)"`.

### Task 10: runner delta (b) — `EvtToolFailed` emission

**Files:**
- Modify: `backend/internal/runtime/runner_eino.go` `drain` (~`:247-254`)
- Test: `backend/internal/runtime/runner_failed_tool_test.go`

`EvtToolFailed` (`domain/event.go:25`) is declared but never emitted; the tool-error branch of `drain` today only emits run-level `EvtModelFailed`/`EvtRunFailed`.

- [ ] **Step 1: Write failing test** — script a tool that errors; assert the runner's event channel carries an `EvtToolFailed` for that tool (before the run-fail path).
- [ ] **Step 2: Run fail** — no `EvtToolFailed` emitted.
- [ ] **Step 3: Implement** — in `drain`'s tool-error path (where `ev.Err != nil` and it is a tool error, `runner_eino.go:247-254`), add `r.emit(out, runID, domain.EvtToolFailed, ev.Err.Error(), toolName, "")` **before** the existing run-fail emission. Do **not** change the run-fail behavior (v1 still fails the run; §9 non-goal).
- [ ] **Step 4: Run pass** — `go test ./internal/runtime/ -run TestEmitToolFailed -v`.
- [ ] **Step 5: Commit** — `git commit -m "feat(runtime): emit EvtToolFailed in drain (delta b, run-fail behavior unchanged)"`.

---

## Chunk 4: assembly + `/settings/tools` API + `ToolConfigProvider`

### Task 11: `ToolConfigProvider` interface + `httpapi` wiring

**Files:**
- Modify: `backend/internal/api/http/server.go` (add interface + field + routes + handlers)
- Test: `backend/internal/api/http/server_tools_test.go`

The HTTP layer must not import `internal/mcp` or `internal/skill` directly — it takes a provider (§4.6). Define the interface in `httpapi`:
```go
type ToolConfigProvider interface {
	GetToolsStatus(ctx context.Context) (ToolStatus, error)
	SaveTools(ctx context.Context, in ToolConfigIn) error
}
```
with `ToolStatus`/`ToolConfigIn` mirroring §4.6 payloads (skills `{enabled, available:[{name,description}]}`, mcp `{enabled, servers:[{name,status,command,tools:[{name,description}]}]}`). Add `New(... , provider ToolConfigProvider)` and routes:
```go
s.mux.HandleFunc("GET /api/v1/settings/tools", s.handleGetTools)
s.mux.HandleFunc("PUT /api/v1/settings/tools", s.handleUpdateTools)
```
Handlers call the provider; `handleUpdateTools` persists + `SaveTools` triggers reconcile (App impl, Task 12).

- [ ] **Step 1: Write failing test** — a stub provider; `GET /settings/tools` returns the stub's `ToolStatus`; `PUT /settings/tools` calls `SaveTools` and returns 200; 404/shape checks.
- [ ] **Step 2: Run fail** — route not found / `provider` param missing.
- [ ] **Step 3: Implement** — the interface + types + `New` param + `Server` field + 2 routes + 2 handlers.
- [ ] **Step 4: Run pass** — `go test ./internal/api/http/ -run TestTools -v`.
- [ ] **Step 5: Commit** — `git commit -m "feat(api): /settings/tools routes + ToolConfigProvider (decoupled from mcp/skill)"`.

### Task 12: App assembly — Manager + Loader + register + provider impl + Stop

**Files:**
- Modify: `backend/internal/app/app.go` (`Start` ~`:41-92`, `Stop` ~`:99`)
- Create: `backend/internal/app/toolconfig.go` (provider impl)

- [ ] **Step 1: Implement** `Start`:
  - `loader := skill.NewLoader(cfg.SkillsDir)`; **keep the loader** (remove the discarded `_ = …Refresh()` at `app.go:79`); `loader.Refresh()`.
  - `manager := mcp.NewManager(...)`; read current tool config via `mcp.ReadToolConfig(ctx, settings)`; `manager.SpawnAll(servers)`; `registry.Replace("mcp__", mcp.MCPDefinitions(manager.Status(), manager))`.
  - if `skillsEnabled`: `registry.Register(skill.InvokeSkill(loader, 32*1024))`; build available-skill listing and `runner.SetSkillInstructions(listing)`.
  - build a `toolConfigProvider` (implements `httpapi.ToolConfigProvider`: `GetToolsStatus` reads settings + `manager.Status()` + `loader.List()`; `SaveTools` persists the 3 keys, then `manager.Reconcile(...)` + `registry.Replace("mcp__", ...)` and, on skills toggle, `registry.Register`/`registry.Remove("invoke_skill")` + `runner.SetSkillInstructions`).
  - pass the provider into `httpapi.New(...)`.
- [ ] **Step 2: Implement** `Stop`: `manager.StopAll(ctx)` before/with `a.srv.Shutdown`.
- [ ] **Step 3: Build + run** — `cd backend && go build ./... && go vet ./internal/app/ ./internal/api/http/`.
- [ ] **Step 4: Smoke test** — `make run`, then `curl localhost:8080/api/v1/settings/tools` (expect skills/mcp shape with mcp status empty or the fake/real servers), and `PUT` a `mcp.servers` value to see reconcile.
- [ ] **Step 5: Commit** — `git commit -m "feat(app): wire MCP Manager + skill Loader + ToolConfigProvider + StopAll"`.

---

## Chunk 5: frontend "Tools" panel + integration tests + docs

### Task 13: Settings "Tools" panel

**Files:**
- Create: `frontend/src/components/ToolsPanel.tsx`
- Modify: `frontend/src/components/SettingsDialog.tsx` (mount ToolsPanel)
- Modify: `frontend/src/lib/api.ts` + `frontend/src/lib/types.ts` (add `getTools`/`saveTools` + `ToolStatus`/`ToolConfigIn` types)

- [ ] **Step 1: Add API** — `api.getTools()` → `GET /settings/tools`; `api.saveTools(cfg)` → `PUT /settings/tools`; types mirroring §4.6.
- [ ] **Step 2: Build ToolsPanel** — Skills sub-panel (toggle `skillsEnabled` + read-only list of `skills.available`); MCP sub-panel (toggle `mcpEnabled`, server list editor, **per-server status badge** from `servers[].status`, per-server tool list from `servers[].tools`). Badge is driven by the REST fetch on open + after save (§4.5 — no live SSE).
- [ ] **Step 3: Mount in SettingsDialog** — add a "Tools" section.
- [ ] **Step 4: Build + type-check** — `cd frontend && npm run build` (or `tsc -b`); run any component tests.
- [ ] **Step 5: Commit** — `git commit -m "feat(frontend): Settings Tools panel (skills + MCP status badge via REST)"`.

### Task 14: integration tests (harness)

**Files:**
- Modify: `backend/tests/testutil/harness.go` (register `invoke_skill` + a fake MCP server + provider)
- Create: `backend/tests/integration/toolconfig_test.go`

- [ ] **Step 1: Wire harness** — register `invoke_skill`; add one fake MCP server (reuse the `internal/mcp` fake via a testable seam, or a canned stdio subprocess) so runs can call `mcp__<srv>__<tool>`; expose a `ToolConfigProvider` stub.
- [ ] **Step 2: Write tests**
  - a run whose model calls a read-only MCP tool → `tool.completed` event + child-free parent succeeds.
  - a run whose model calls an `external/always` MCP tool → `waiting_approval` → `decide` → `resume` (mirror `approval_test.go`).
  - `invoke_skill` returns the SKILL.md body as tool output.
  - `GET /settings/tools` shape; `PUT` persists + reconciles.
- [ ] **Step 3: Run** — `cd backend && go test ./tests/integration/ -run 'TestMCP|TestInvokeSkill|TestTools' -v`.
- [ ] **Step 4: Commit** — `git commit -m "test(integration): MCP tool call + approval + invoke_skill + /settings/tools"`.

### Task 15: full regression + docs

- [ ] **Step 1: Full suite** — `cd backend && go build ./... && go vet ./... && go test ./...` (expect green; `git diff --name-only` shows no unintended Go/frontend cross-contamination per acceptance).
- [ ] **Step 2: Docs** — add a "Tool configuration" section to `backend/README.md` (settings keys, stdio server example, default-high-risk + empty-scopes invariant, `invoke_skill`).
- [ ] **Step 3: Commit** — `git commit -m "docs: tool configuration (skill + MCP + global config)"`.

---

## Cross-cutting notes

- **Security invariant (§4.3):** `mcp__` and `invoke_skill` defs MUST have empty `RequiredScopes` (gateway tool passes nil scopes → `CheckPolicy` hard-denies any non-empty scope; see `policy.go:17-27`). `external/always` MCP tools → `waiting_approval` card, exactly like `workspace_write`.
- **Runner deltas are only (a) + (b)** — no new execution path.
- **v1 non-goals (§9):** auto-respawn, non-fatal tool calls, HTTP/SSE transport, per-session/per-skill flags, secret expansion, per-tool timeouts.
- **Chunk size** stays <1000 lines each; run the plan-document-reviewer after each chunk per the skill's review loop.
