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
- `backend/internal/mcp/transport.go` — `Transport` interface + `StdioTransport` (real child process) + `StdioTransportFactory`
- `backend/internal/mcp/fake.go` — exported in-memory `FakeTransport` (canned JSON-RPC, up/crash modes) for client/Manager tests + `FakeTransportFactory`
- `backend/internal/mcp/client.go` — one-server JSON-RPC client (`initialize`/`tools/list`/`tools/call`)
- `backend/internal/mcp/manager.go` — `Manager` (`SpawnAll`/`Reconcile`/`StopAll`/`Status`/`Call`) + `StatusEntry` + `TransportFactory`
- `backend/internal/mcp/bridge.go` — `MCPDefinitions` → `[]tool.Definition` (external/always, empty RequiredScopes)
- `backend/internal/mcp/config.go` — `ServerSpec`, `ToolConfig`, settings keys + encode/decode
- `backend/internal/skill/skill_tool.go` — `InvokeSkill` `tool.Definition`
- `backend/internal/app/toolconfig.go` — `ToolConfigProvider` impl (App owns SettingsRepo + Manager + Loader)
- `backend/tests/integration/toolconfig_test.go` — integration tests
- `frontend/src/components/ToolsPanel.tsx` + settings store slice

Modified files:
- `backend/internal/runtime/runner_eino.go` + `runner_child.go` — guarded instruction setter + `EvtToolFailed` emission
- `backend/internal/app/app.go` — construct Manager + wire Loader + register tools + `Stop`→`StopAll` + pass provider option
- `backend/internal/api/http/server.go` — **variadic `Option`** on `New` (backward-compatible; existing 7-arg calls still compile) + `WithToolConfigProvider` + `/settings/tools` routes/handlers
- `backend/tests/testutil/harness.go` — register `invoke_skill` + a **canned `mcp__` tool definition** (no real MCP process) + provider stub
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

// getStr reads a settings key; a read error is treated as "absent" (empty)
// on purpose — a missing mcp.servers key means "no MCP servers", not a failure.
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

### Task 3: `Transport` interface + `StdioTransport` + `StdioTransportFactory`

**Files:**
- Create: `backend/internal/mcp/transport.go`
- Test: `backend/internal/mcp/transport_test.go`

One JSON-RPC message per line over a child process (its own process group for clean kill). The `Transport` interface is the seam that makes the whole thing testable without a subprocess: the Manager is built against a `TransportFactory`, and tests inject the in-memory `FakeTransport` from Task 4.

- [ ] **Step 1: Write the failing test** — `backend/internal/mcp/transport_test.go`:
```go
package mcp

import (
	"context"
	"strings"
	"testing"
)

// /bin/cat is deterministic: it echoes each stdin line to stdout verbatim.
func TestStdioTransportRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("requires /bin/cat")
	}
	tt, err := NewStdioTransport(context.Background(), "/bin/cat", nil)
	if err != nil {
		t.Fatalf("start /bin/cat: %v", err)
	}
	defer func() { _ = tt.Stop() }()
	if err := tt.WriteLine([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	line, err := tt.ReadLine()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.TrimSpace(string(line)) != "ping" {
		t.Fatalf("round-trip = %q, want ping", line)
	}
}
```
- [ ] **Step 2: Run to verify fail** — `cd backend && go test ./internal/mcp/ -run TestStdioTransportRoundTrip -v` → `undefined: NewStdioTransport`.
- [ ] **Step 3: Implement** — `backend/internal/mcp/transport.go`:
```go
package mcp

import (
	"bufio"
	"context"
	"io"
	"os/exec"
	"syscall"
)

// Transport is one MCP server connection. WriteLine sends a JSON-RPC message
// (a single line); ReadLine returns one response line. The seam lets tests
// substitute an in-memory fake for the real stdio child process.
type Transport interface {
	WriteLine(p []byte) error
	ReadLine() ([]byte, error) // io.EOF when the server closed
	Stop() error
}

// TransportFactory builds a Transport for a configured server.
type TransportFactory interface {
	New(ctx context.Context, spec ServerSpec) (Transport, error)
}

// StdioTransport runs one MCP server as a child process in its own process
// group so Stop() can kill the whole group.
type StdioTransport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
}

func NewStdioTransport(ctx context.Context, command string, args []string) (*StdioTransport, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &StdioTransport{cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout)}, nil
}

func (t *StdioTransport) WriteLine(p []byte) error {
	if _, err := t.stdin.Write(p); err != nil {
		return err
	}
	_, err := t.stdin.Write([]byte("\n"))
	return err
}

func (t *StdioTransport) ReadLine() ([]byte, error) {
	return t.stdout.ReadBytes('\n')
}

func (t *StdioTransport) Stop() error {
	_ = t.stdin.Close()
	if t.cmd.Process != nil {
		_ = syscall.Kill(-t.cmd.Process.Pid, syscall.SIGKILL)
		_, _ = t.cmd.Process.Wait()
	}
	return nil
}

// StdioTransportFactory is the production TransportFactory.
type StdioTransportFactory struct{}

func NewStdioTransportFactory() *StdioTransportFactory { return &StdioTransportFactory{} }

func (f *StdioTransportFactory) New(ctx context.Context, spec ServerSpec) (Transport, error) {
	return NewStdioTransport(ctx, spec.Command, spec.Args)
}
```
(UNIX-only, matching the project target. `ServerSpec` is from Task 2's `config.go`.)
- [ ] **Step 4: Run to verify pass** — `cd backend && go test ./internal/mcp/ -run TestStdioTransportRoundTrip -v` → PASS.
- [ ] **Step 5: Commit** — `git add backend/internal/mcp/transport.go backend/internal/mcp/transport_test.go && git commit -m "feat(mcp): Transport interface + StdioTransport (child process group) + factory"`

### Task 4: in-memory `FakeTransport` (exported test seam)

**Files:**
- Create: `backend/internal/mcp/fake.go` (exported, non-test — importable by `tests/integration`)
- Create: `backend/internal/mcp/fake_test.go`

`FakeTransport` is a `Transport` that answers canned JSON-RPC without a subprocess. It is **exported and in a non-test file** so both `internal/mcp` tests and `tests/integration` can import it (a `_test.go`-only fake can't be imported across packages). It has two modes: **up** (canned `initialize`/`tools/list`/`tools/call`) and **crash** (`ReadLine` immediately errors, simulating a disconnected server).

(TDD order note: Task 4 Step 1 is "Implement" rather than "Write failing test" — the `FakeTransport` is the test seam that *later* tasks' failing tests depend on, so it ships with a smoke test (Step 2) rather than a red test first. This is an intentional, documented deviation from the fail-first pattern, not a violation of the chunk green-gate: the chunk still compiles + tests green at each commit.)

- [ ] **Step 1: Implement** — `backend/internal/mcp/fake.go`:
```go
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// FakeTransport is an in-memory Transport for tests. It captures each request
// (method + id) on WriteLine and returns the canned response for that method on
// the next ReadLine. The client drives one request at a time, so a single
// "last request" slot is enough for correlation.
type FakeTransport struct {
	Tools      []string // names returned by tools/list
	CallResult string   // text returned for a successful tools/call
	CallError  bool     // if true, tools/call returns isError
	Crash      bool     // if true, the server is disconnected

	mu      sync.Mutex
	pending *jsonRequest
}

type jsonRequest struct {
	ID     int             `json:"id"`
	Method string         `json:"method"`
	Params json.RawMessage `json:"params"`
}

// FakeTransportFactory builds FakeTransports for a named server spec.
type FakeTransportFactory struct {
	ByServer map[string]*FakeTransport
}

func NewFakeTransportFactory() *FakeTransportFactory {
	return &FakeTransportFactory{ByServer: map[string]*FakeTransport{}}
}

func (f *FakeTransportFactory) New(_ context.Context, spec ServerSpec) (Transport, error) {
	if ft, ok := f.ByServer[spec.Name]; ok {
		return ft, nil
	}
	ft := &FakeTransport{Tools: []string{"echo"}}
	f.ByServer[spec.Name] = ft
	return ft, nil
}

// WriteLine decodes the request method + id and holds it for the next ReadLine.
func (ft *FakeTransport) WriteLine(p []byte) error {
	if ft.Crash {
		return fmt.Errorf("mcp fake: crashed")
	}
	var req jsonRequest
	if err := json.Unmarshal(p, &req); err != nil {
		return fmt.Errorf("mcp fake: bad request: %w", err)
	}
	ft.mu.Lock()
	ft.pending = &req
	ft.mu.Unlock()
	return nil
}

// ReadLine emits the canned JSON-RPC response for the last request method.
func (ft *FakeTransport) ReadLine() ([]byte, error) {
	if ft.Crash {
		return nil, fmt.Errorf("mcp fake: disconnected")
	}
	ft.mu.Lock()
	req := ft.pending
	ft.pending = nil
	ft.mu.Unlock()
	if req == nil {
		return nil, fmt.Errorf("mcp fake: ReadLine before WriteLine")
	}
	body, err := ft.cannedFor(req)
	if err != nil {
		return nil, err
	}
	return []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":%s}`, req.ID, body)), nil
}

// cannedFor builds the "result" JSON body for a request.
func (ft *FakeTransport) cannedFor(req *jsonRequest) (string, error) {
	switch req.Method {
	case "initialize":
		return `{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"fake"}}`, nil
	case "tools/list":
		type mcpTool struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		tools := make([]mcpTool, 0, len(ft.Tools))
		for _, n := range ft.Tools {
			tools = append(tools, mcpTool{Name: n, Description: "fake tool"})
		}
		b, err := json.Marshal(map[string]any{"tools": tools})
		return string(b), err
	case "tools/call":
		if ft.CallError {
			return `{"content":[{"type":"text","text":"boom"}],"isError":true}`, nil
		}
		b, _ := json.Marshal(map[string]any{
			"content": []any{map[string]string{"type": "text", "text": ft.CallResult}},
		})
		return string(b), nil
	default:
		return "", fmt.Errorf("mcp fake: unknown method %q", req.Method)
	}
}

func (ft *FakeTransport) Stop() error { return nil }
```
(Import `context`, `encoding/json`, `fmt`, `sync`; `ServerSpec` is from Task 2's `config.go`. The factory's default server returns one tool `"echo"`; tests override `ByServer` with their own `Tools`.)

- [ ] **Step 2: Add a smoke test** — `backend/internal/mcp/fake_test.go`:
```go
package mcp

import (
	"context"
	"testing"
)

func TestFakeTransportFactory(t *testing.T) {
	f := NewFakeTransportFactory()
	f.ByServer["fs"] = &FakeTransport{Tools: []string{"read"}, CallResult: "ok"}
	tt, err := f.New(context.Background(), ServerSpec{Name: "fs", Command: "npx"})
	if err != nil {
		t.Fatal(err)
	}
	if ft, ok := tt.(*FakeTransport); !ok || ft != f.ByServer["fs"] {
		t.Fatalf("factory must return the configured fake, got %T", tt)
	}
}
```
- [ ] **Step 3: Run** — `cd backend && go test ./internal/mcp/ -run TestFakeTransportFactory -v` → PASS.
- [ ] **Step 4: Commit** — `git add backend/internal/mcp/fake.go backend/internal/mcp/fake_test.go && git commit -m "feat(mcp): exported in-memory FakeTransport + factory (up/crash modes)"`

### Task 5: JSON-RPC `client`

**Files:**
- Create: `backend/internal/mcp/client.go`
- Test: `backend/internal/mcp/client_test.go`

Speaks the three MCP methods over a `Transport`. Concrete wire shapes (one JSON-RPC message per line):

| step | request (`method`, `id`) | response shape |
|---|---|---|
| initialize | `{"jsonrpc":"2.0","id":N,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"rivus"}}}` | `result{protocolVersion, capabilities, serverInfo}` |
| tools/list | `{"jsonrpc":"2.0","id":N,"method":"tools/list","params":{}}` | `result.tools[] = {name, description, inputSchema}` |
| tools/call | `{"jsonrpc":"2.0","id":N,"method":"tools/call","params":{"name":"<tool>","arguments":{...}}}` | `result.content[] = {type:"text", text}`, optional `isError` |

Request `id` is a monotonically-incrementing counter (one in flight at a time per client); the response's `id` must match before the call returns. `initialize` and `tools/list` run under a 5s context deadline.

- [ ] **Step 1: Write failing test** — `backend/internal/mcp/client_test.go` (uses `FakeTransport` from Task 4):
```go
package mcp

import (
	"context"
	"strings"
	"testing"
)

func TestClientListAndCall(t *testing.T) {
	ft := &FakeTransport{Tools: []string{"echo", "sum"}, CallResult: "42"}
	c := NewClient(ft)
	if err := c.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	tools, err := c.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v", tools)
	}
	out, err := c.Call(context.Background(), "echo", map[string]any{"x": 1})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if out != "42" {
		t.Fatalf("call output = %q, want 42", out)
	}
	// isError result maps to a Go error
	ft.CallError = true
	if _, err := c.Call(context.Background(), "echo", nil); err == nil || !strings.Contains(err.Error(), "mcp") {
		t.Fatalf("isError must map to error, got %v", err)
	}
}
```
- [ ] **Step 2: Run to verify fail** — `cd backend && go test ./internal/mcp/ -run TestClientListAndCall -v` → `undefined: NewClient`.
- [ ] **Step 3: Implement** — `backend/internal/mcp/client.go`:
```go
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
)

// Tool is one entry from an MCP tools/list result.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
}

// Client drives one MCP server over a Transport (initialize → tools/list → tools/call).
type Client struct {
	transport Transport
	id        int32
}

func NewClient(t Transport) *Client { return &Client{transport: t} }

func (c *Client) nextID() int { return int(atomic.AddInt32(&c.id, 1)) }

// rpc sends one JSON-RPC request and returns its response body (the "result" field).
func (c *Client) rpc(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID()
	req, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	if err := c.transport.WriteLine(req); err != nil {
		return nil, fmt.Errorf("mcp write %s: %w", method, err)
	}
	line, err := c.transport.ReadLine()
	if err != nil {
		return nil, fmt.Errorf("mcp server disconnected reading %s: %w", method, err)
	}
	var resp struct {
		ID     int             `json:"id"`
		Err    *struct {
			Message string `json:"message"`
		} `json:"error"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("mcp unmarshal %s: %w", method, err)
	}
	if resp.Err != nil {
		return nil, fmt.Errorf("mcp %s: %s", method, resp.Err.Message)
	}
	return resp.Result, nil
}

func (c *Client) Initialize(ctx context.Context) error {
	_, err := c.rpc(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "rivus"},
	})
	return err
}

func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	res, err := c.rpc(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var out struct {
		Tools []Tool `json:"tools"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return nil, err
	}
	return out.Tools, nil
}

// Call invokes tools/call and maps the content blocks to a joined string.
// A result with isError=true is returned as a Go error.
func (c *Client) Call(ctx context.Context, name string, arguments map[string]any) (string, error) {
	res, err := c.rpc(ctx, "tools/call", map[string]any{"name": name, "arguments": arguments})
	if err != nil {
		return "", err
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return "", err
	}
	var sb []string
	for _, b := range out.Content {
		if b.Type == "text" {
			sb = append(sb, b.Text)
		}
	}
	if out.IsError {
		return "", fmt.Errorf("mcp tool %s error: %s", name, joinText(sb))
	}
	return joinText(sb), nil
}

func joinText(p []string) string {
	s := ""
	for i, v := range p {
		if i > 0 {
			s += "\n"
		}
		s += v
	}
	return s
}
```

**Important (fixes the ordering + double-wrap concerns):** the `FakeTransport` in Task 4 must actually answer the client's three method calls in sequence — that's why Task 4 (fake) precedes Task 5 (client). And note the `Handler` from the bridge (Task 7) will NOT add its own timeout: the executor already wraps every tool call with `context.WithTimeout(def.TimeoutMs)` (`tool/executor.go:73-75`), so the `mcp__` def just sets `TimeoutMs: 30000` and relies on that.

- [ ] **Step 4: Run to verify pass** — `cd backend && go test ./internal/mcp/ -run TestClientListAndCall -v` → PASS.
- [ ] **Step 5: Commit** — `git add backend/internal/mcp/client.go backend/internal/mcp/client_test.go && git commit -m "feat(mcp): JSON-RPC client (initialize/tools list/call, content block->string, isError->error)"`

### Task 6: `Manager` (`StatusEntry`, `SpawnAll`, `Reconcile`, `StopAll`, `Call`)

**Files:**
- Create: `backend/internal/mcp/manager.go`
- Test: `backend/internal/mcp/manager_test.go`

The Manager owns connections + per-server status only. It does NOT touch the `tool.Registry` — the single registry-mutation call site is Task 12 (App assembly / provider), per spec §4.1 "Task 12 is the sole `registry.Replace` call site".

**Types** (define in `manager.go`):
```go
package mcp

import (
	"context"
	"fmt"
	"sync"
)

// StatusEntry is one configured MCP server's live state (feeds GET /settings/tools).
type StatusEntry struct {
	Name    string    `json:"name"`
	Command string    `json:"command"`
	Status  string    `json:"status"` // "up" | "down" | "error"
	Err     string    `json:"err,omitempty"`
	Tools   []Tool    `json:"tools"` // cached tools/list result
}

// Manager owns all live MCP server connections.
type Manager struct {
	mu     sync.RWMutex
	factory TransportFactory
	clis   map[string]*Client // keyed by server name
	status map[string]StatusEntry
}

// NewManager builds a Manager over a TransportFactory (stdio in prod, fake in tests).
func NewManager(factory TransportFactory) *Manager {
	return &Manager{factory: factory, clis: map[string]*Client{}, status: map[string]StatusEntry{}}
}
```

- [ ] **Step 1: Write failing test** — `backend/internal/mcp/manager_test.go`:
```go
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
```
- [ ] **Step 2: Run to verify fail** — `cd backend && go test ./internal/mcp/ -run TestManagerLifecycle -v` → `undefined: (NewManager/SpawnAll/…)`.
- [ ] **Step 3: Implement** — complete `manager.go` methods:
```go
// SpawnAll connects to every server in specs; a failed initialize marks it "error"
// (logged, others continue). Idempotent for servers already connected.
func (m *Manager) SpawnAll(ctx context.Context, specs []ServerSpec) error {
	for _, s := range specs {
		if _, ok := m.clientFor(s.Name); ok {
			continue
		}
		m.connect(ctx, s)
	}
	return nil
}

// Reconcile stops servers not in specs and spawns new ones. It does NOT mutate
// the tool registry (Task 12 is that call site).
func (m *Manager) Reconcile(ctx context.Context, specs []ServerSpec) error {
	keep := map[string]bool{}
	for _, s := range specs {
		keep[s.Name] = true
	}
	m.mu.Lock()
	for name := range m.clis {
		if !keep[name] {
			_ = m.clis[name].transport.Stop()
			delete(m.clis, name)
			delete(m.status, name)
		}
	}
	m.mu.Unlock()
	for _, s := range specs {
		if _, ok := m.clientFor(s.Name); !ok {
			m.connect(ctx, s)
		}
	}
	return nil
}

func (m *Manager) connect(ctx context.Context, s ServerSpec) {
	tt, err := m.factory.New(ctx, s)
	if err != nil {
		m.setStatus(s.Name, s.Command, "error", err.Error())
		return
	}
	c := NewClient(tt)
	ictx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := c.Initialize(ictx); err != nil {
		m.setStatus(s.Name, s.Command, "error", "initialize: "+err.Error())
		return
	}
	tools, err := c.ListTools(ictx)
	if err != nil {
		m.setStatus(s.Name, s.Command, "error", "tools/list: "+err.Error())
		return
	}
	m.mu.Lock()
	m.clis[s.Name] = c
	m.status[s.Name] = StatusEntry{Name: s.Name, Command: s.Command, Status: "up", Tools: tools}
	m.mu.Unlock()
}

// Call invokes tools/call on a server. A dead connection returns a "disconnected" error.
func (m *Manager) Call(ctx context.Context, server, tool string, args map[string]any) (string, error) {
	c, ok := m.clientFor(server)
	if !ok {
		return "", fmt.Errorf("mcp server %s disconnected", server)
	}
	out, err := c.Call(ctx, tool, args)
	if err != nil {
		return "", fmt.Errorf("mcp server %s disconnected: %w", server, err)
	}
	return out, nil
}

func (m *Manager) clientFor(name string) (*Client, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.clis[name]
	return c, ok
}

// Status returns a snapshot of every configured server's state.
func (m *Manager) Status() []StatusEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]StatusEntry, 0, len(m.status))
	for _, e := range m.status {
		out = append(out, e)
	}
	return out
}

func (m *Manager) setStatus(name, command, status, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := StatusEntry{Name: name, Command: command, Status: status}
	if errMsg != "" {
		e.Err = errMsg
	}
	m.status[name] = e
}

// StopAll stops every connection and clears status.
func (m *Manager) StopAll() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, c := range m.clis {
		_ = c.transport.Stop()
		delete(m.status, name)
	}
	m.clis = map[string]*Client{}
	return nil
}
```
(The `Client.transport` field is already defined in Task 5's `client.go` — `type Client struct { transport Transport; id int32 }` — so the Manager's `m.clis[...].transport.Stop()` works as-is. Add the `time` import to `manager.go` for the 5s `context.WithTimeout`.)
- [ ] **Step 4: Run to verify pass** — `cd backend && go test ./internal/mcp/ -run TestManagerLifecycle -v` → PASS.
- [ ] **Step 5: Commit** — `git add backend/internal/mcp/manager.go backend/internal/mcp/manager_test.go && git commit -m "feat(mcp): Manager (spawn/reconcile/status/call) with StatusEntry; registry mutation stays in app layer"`

### Task 7: MCP → `tool.Definition` bridge

**Files:**
- Create: `backend/internal/mcp/bridge.go`
- Test: `backend/internal/mcp/bridge_test.go`

**Data-flow (single path):** `Manager.Status()` → `MCPDefinitions(status, manager)` → `[]tool.Definition` → Task 12 calls `registry.Replace("mcp__", defs)`. The Manager itself never touches the registry.

- [ ] **Step 1: Write failing test** — `backend/internal/mcp/bridge_test.go`:
```go
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
```
- [ ] **Step 2: Run to verify fail** — `cd backend && go test ./internal/mcp/ -run TestMCPDefinitions -v` → `undefined: MCPDefinitions`.
- [ ] **Step 3: Implement** — `backend/internal/mcp/bridge.go`:
```go
package mcp

import (
	"context"
	"encoding/json"

	"rivus-agent-backend/internal/tool"
)

// MCPDefinitions builds one tool.Definition per discovered MCP tool, named
// mcp__<server>__<tool>. Invariant (spec §4.3): Risk=external, Approval=always,
// RequiredScopes EMPTY (the gateway passes nil scopes, so any scope hard-denies).
// Handler delegates to Manager.Call; the executor already applies TimeoutMs, so
// the handler does NOT add its own context deadline.
func MCPDefinitions(status []StatusEntry, mgr *Manager) []tool.Definition {
	var defs []tool.Definition
	for _, e := range status {
		if e.Status != "up" {
			continue // only live servers expose tools
		}
		for _, tl := range e.Tools {
			name := "mcp__" + e.Name + "__" + tl.Name
			server, t := e.Name, tl.Name
			defs = append(defs, tool.Definition{
				Name:        name,
				Description: tl.Description,
				Version:     "v1",
				InputSchema: inputSchemaFor(tl),
				Risk:        tool.RiskExternal,
				Approval:    tool.ApprovalAlways,
				TimeoutMs:   30000,
				MaxResultBytes: 64 * 1024,
				Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
					var a map[string]any
					_ = json.Unmarshal(raw, &a)
					return mgr.Call(ctx, server, t, a)
				},
			})
		}
	}
	return defs
}

func inputSchemaFor(t Tool) string {
	if t.InputSchema != nil {
		if b, err := json.Marshal(t.InputSchema); err == nil {
			return string(b)
		}
	}
	return `{"type":"object"}`
}
```
- [ ] **Step 4: Run to verify pass** — `cd backend && go test ./internal/mcp/ -run TestMCPDefinitions -v` → PASS.
- [ ] **Step 5: Commit** — `git add backend/internal/mcp/bridge.go backend/internal/mcp/bridge_test.go && git commit -m "feat(mcp): bridge MCP tools to tool.Definition (external/always, empty scopes, mangled names)"`

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
- [ ] **Step 3: Implement** — the setter must be **idempotent** (Task 12 calls it on every enable-save and with `""` on disable; accumulating would duplicate the skills section). Store the base instruction once and **replace** the skills section on every call:
```go
// baseInstruction is set once in NewEinoRunner (the existing instruction value).
// SetSkillInstructions rebuilds the full instruction as base + optional skills section,
// so repeated enables don't accumulate and "" removes the section.
func (r *EinoRunner) SetSkillInstructions(listing string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if listing == "" {
		r.instruction = r.baseInstruction
		return
	}
	r.instruction = r.baseInstruction + "\n\n可用技能：\n" + listing
}
```
Add the `baseInstruction string` field to `EinoRunner` and set it in `NewEinoRunner` (same place the existing instruction literal is assigned); also initialize `r.instruction = r.baseInstruction` there. In `buildAgentFiltered`/`buildAgent`, read `r.instruction` under `r.mu.RLock()`. (The runner's existing `mu` already guards `needApprovalByRun`; reuse it.)
- [ ] **Step 4: Run pass** — `go test -race ./internal/runtime/ -run TestInstruction -v`.
- [ ] **Step 5: Commit** — `git commit -m "feat(runtime): guarded skill-name instruction augmentation (delta a)"`.

### Task 10: runner delta (b) — `EvtToolFailed` emission

**Files:**
- Modify: `backend/internal/runtime/runner_eino.go` `drain` (~`:247-254`)
- Test: `backend/internal/runtime/runner_failed_tool_test.go`

`EvtToolFailed` (`domain/event.go:25`) is declared but never emitted; the tool-error branch of `drain` today only emits run-level `EvtModelFailed`/`EvtRunFailed`.

- [ ] **Step 1: Write failing test** — `backend/internal/runtime/runner_failed_tool_test.go`. **Do NOT import `tests/testutil` here** — `testutil` imports `internal/runtime` (harness exposes `Runner *runtime.EinoRunner`), so an in-package `package runtime` test that imports `testutil` is an import cycle. Instead build a tiny local stub `ToolCallingChatModel` (satisfies `einomodel.ToolCallingChatModel`: one scripted tool-call that makes the gateway tool return a `domain.Wrap("tool_failed", …)` error) and drive `runner.drain` through it. Assert the `out` channel carries an `EvtToolFailed` (before `EvtRunFailed`).
- [ ] **Step 2: Run fail** — `cd backend && go test ./internal/runtime/ -run TestEmitToolFailed -v` → no `EvtToolFailed` emitted (FAIL).
- [ ] **Step 3: Implement** — a failed tool call surfaces in `drain` as a `*domain.CodedError{Code:"tool_failed"}` (the executor wraps every handler error this way, `tool/executor.go:82`). It carries **no tool-name field**, so the exact failing tool is not recoverable — track a best-effort `lastTool` (the most recent `mo.ToolName` seen) as a hint. Concretely, in `drain` (`runner_eino.go:231-281`):
  1. Next to `var lastText string`, add `lastTool := ""`.
  2. In the `mo.ToolName != ""` branch (existing `runner_eino.go:269-271`), set `lastTool = mo.ToolName` before the existing `EvtToolCompleted` emit.
  3. In the `if ev.Err != nil` branch (existing `runner_eino.go:247-254`), **before** the `EvtModelFailed`/`EvtRunFailed` emissions, add:
```go
var ce *domain.CodedError
if errors.As(ev.Err, &ce) && ce.Code == "tool_failed" {
	r.emit(out, runID, domain.EvtToolFailed, ev.Err.Error(), lastTool, "")
}
```
  4. **Do NOT change the run-fail behavior** (v1 still emits `EvtModelFailed` + `EvtRunFailed` and returns; §9 non-goal). Add the `errors` import if not present.
- [ ] **Step 4: Run pass** — `cd backend && go test ./internal/runtime/ -run TestEmitToolFailed -v` → PASS.
- [ ] **Step 5: Commit** — `git add backend/internal/runtime/runner_eino.go backend/internal/runtime/runner_failed_tool_test.go && git commit -m "feat(runtime): emit EvtToolFailed in drain for tool_failed errors (run-fail behavior unchanged)"`.

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

// ToolStatus is the GET /settings/tools payload (mirrors §4.6; per-server shape
// matches mcp.StatusEntry but the httpapi layer defines its own type so it
// doesn't import internal/mcp).
type ToolStatus struct {
	Skills SkillsStatus   `json:"skills"`
	MCP    MCPStatus      `json:"mcp"`
}
type SkillsStatus struct {
	Enabled   bool                 `json:"enabled"`
	Available []SkillInfo          `json:"available"`
}
type SkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}
type MCPStatus struct {
	Enabled bool            `json:"enabled"`
	Servers []MCPServerInfo `json:"servers"`
}
type MCPServerInfo struct {
	Name    string   `json:"name"`
	Status  string   `json:"status"` // "up" | "down" | "error"
	Command string   `json:"command"`
	Tools   []ToolInfo `json:"tools"`
}
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ToolConfigIn is the PUT /settings/tools payload.
type ToolConfigIn struct {
	SkillsEnabled bool           `json:"skillsEnabled"`
	MCPEnabled    bool           `json:"mcpEnabled"`
	MCPServers   []MCPServerIn `json:"mcpServers"`
}
type MCPServerIn struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}
```

**`httpapi.New` signature change — variadic `Option` (backward-compatible):** the current `New` takes 7 positional args (`server.go:37`). Adding a required 8th arg breaks every existing `httpapi.New(...)` call site (`app.go:83`, `harness.go`, any tests) until they're all updated, which violates the chunk-compiles-green invariant. Use the functional-options pattern instead:
```go
// Option configures an httpapi.Server. Existing 7-arg New(...) calls stay
// valid; opt in to the tools API via WithToolConfigProvider(...).
type Option func(*serverOptions)
type serverOptions struct {
	tools ToolConfigProvider
}
func WithToolConfigProvider(p ToolConfigProvider) Option {
	return func(o *serverOptions) { o.tools = p }
}

// New is unchanged in its 7 positional args + optional trailing Options:
func New(cfg config.Config, db *sql.DB, tasks *store.TaskRepo, events *store.EventRepo,
	settings *store.SettingsRepo, runs *service.RunService, sessions *service.SessionService,
	opts ...Option) *Server {
	o := serverOptions{}
	for _, opt := range opts {
		opt(&o)
	}
	s := &Server{ /* …existing fields… */ tools: o.tools }
	s.routes()
	return s
}
```

Routes (add inside `routes()`; the tool handlers are no-ops that return a structured 501 when no provider was configured, so they don't break a bare 7-arg `New` call):
```go
s.mux.HandleFunc("GET /api/v1/settings/tools", s.handleGetTools)
s.mux.HandleFunc("PUT /api/v1/settings/tools", s.handleUpdateTools)
```
- `handleGetTools`: if `s.tools == nil` → `writeErr(w, http.StatusNotImplemented, "tools_not_configured", "tools provider not set on this server", false)`; else call `s.tools.GetToolsStatus(r.Context())` → 200 `ToolStatus`.
- `handleUpdateTools`: if `s.tools == nil` → same `writeErr(..., 501, "tools_not_configured", …)`; else decode `ToolConfigIn`, call `s.tools.SaveTools(r.Context(), in)` → 200 `{}`. (Use the existing `writeErr` shape — `{code,message,retryable}` — not a bare JSON error, to match `server.go:84-88`.)

Handlers call the provider; `SaveTools` persists + triggers reconcile (implemented by the App, Task 12).

- [ ] **Step 1: Write failing test** — `backend/internal/api/http/server_tools_test.go`: build a `Server` via the 7-arg `New` **with** `WithToolConfigProvider(stub)` where `stub.GetToolsStatus` returns a fixed `ToolStatus` and `stub.SaveTools` records the `ToolConfigIn`. Assert: `GET /api/v1/settings/tools` → 200 + the fixed `ToolStatus`; `PUT /api/v1/settings/tools` → 200 + the stub saw the payload. Also assert a bare 7-arg `New` (no option) → `GET` returns 501.
- [ ] **Step 2: Run to verify fail** — `cd backend && go test ./internal/api/http/ -run TestTools -v` → route 404 / `undefined: WithToolConfigProvider`.
- [ ] **Step 3: Implement** — the `ToolConfigProvider` interface + `ToolStatus`/`ToolConfigIn` types + `Option`/`WithToolConfigProvider` + the variadic `New` (backward-compatible) + `Server.tools` field + the two routes + two handlers (501 when `tools == nil`).
- [ ] **Step 4: Run pass** — `go test ./internal/api/http/ -run TestTools -v`.
- [ ] **Step 5: Commit** — `git commit -m "feat(api): /settings/tools routes + ToolConfigProvider (decoupled from mcp/skill)"`.

### Task 12: App assembly — Manager + Loader + register + provider impl + Stop

**Files:**
- Modify: `backend/internal/app/app.go` (`Start` ~`:41-92`, `Stop` ~`:99`)
- Create: `backend/internal/app/toolconfig.go` (provider impl)

- [ ] **Step 1: Implement** `toolconfig.go` (the `httpapi.ToolConfigProvider` impl — complete code):
```go
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"rivus-agent-backend/internal/api/http/httpapi"
	"rivus-agent-backend/internal/mcp"
	"rivus-agent-backend/internal/skill"
	"rivus-agent-backend/internal/store"
	"rivus-agent-backend/internal/tool"
)

// toolConfigProvider implements httpapi.ToolConfigProvider. App owns the
// SettingsRepo, the mcp.Manager, and the skill.Loader, so it bridges them to
// the HTTP layer without the HTTP layer importing mcp/skill.
type toolConfigProvider struct {
	mu       sync.Mutex
	settings *store.SettingsRepo
	manager  *mcp.Manager
	loader   *skill.Loader
	registry *tool.Registry
	runner   runnerInstr // the guarded skill-instruction setter (§6 delta a)
}

type runnerInstr interface {
	SetSkillInstructions(listing string)
}

func newToolConfigProvider(settings *store.SettingsRepo, manager *mcp.Manager,
	loader *skill.Loader, registry *tool.Registry, runner runnerInstr) *toolConfigProvider {
	return &toolConfigProvider{settings: settings, manager: manager, loader: loader,
		registry: registry, runner: runner}
}

func (p *toolConfigProvider) GetToolsStatus(ctx context.Context) (httpapi.ToolStatus, error) {
	tc, err := mcp.ReadToolConfig(ctx, p.settings)
	if err != nil {
		return httpapi.ToolStatus{}, err
	}
	var st httpapi.ToolStatus
	st.Skills.Enabled = tc.SkillsEnabled
	for _, m := range p.loader.List() {
		st.Skills.Available = append(st.Skills.Available, httpapi.SkillInfo{Name: m.Name, Description: m.Description})
	}
	st.MCP.Enabled = tc.MCPEnabled
	for _, e := range p.manager.Status() {
		infos := make([]httpapi.ToolInfo, 0, len(e.Tools))
		for _, tl := range e.Tools {
			infos = append(infos, httpapi.ToolInfo{Name: tl.Name, Description: tl.Description})
		}
		st.MCP.Servers = append(st.MCP.Servers, httpapi.MCPServerInfo{
			Name: e.Name, Status: e.Status, Command: e.Command, Tools: infos,
		})
	}
	return st, nil
}

func (p *toolConfigProvider) SaveTools(ctx context.Context, in httpapi.ToolConfigIn) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// 1) Persist the three settings keys.
	if err := p.settings.Set(ctx, mcp.KeySkillsEnabled, boolStr(in.SkillsEnabled)); err != nil {
		return err
	}
	if err := p.settings.Set(ctx, mcp.KeyMCPEnabled, boolStr(in.MCPEnabled)); err != nil {
		return err
	}
	servers, err := mcpServersFromIn(in)
	if err != nil {
		return err
	}
	if err := p.settings.Set(ctx, mcp.KeyMCP_servers, marshalJSON(servers)); err != nil {
		return err
	}

	// 2) Reconcile MCP connections, then refresh the shared registry's mcp__
	//    family (in-place; the runner holds the *Registry pointer).
	if err := p.manager.Reconcile(ctx, servers); err != nil {
		return err
	}
	if err := p.registry.Replace("mcp__", mcp.MCPDefinitions(p.manager.Status(), p.manager)); err != nil {
		return err
	}

	// 3) Toggle invoke_skill + refresh the skill instruction listing.
	if in.SkillsEnabled {
		_ = p.registry.Remove("invoke_skill")
		if err := p.registry.Register(skill.InvokeSkill(p.loader, 32*1024)); err != nil {
			return err
		}
		p.runner.SetSkillInstructions(skillListing(p.loader))
	} else {
		_ = p.registry.Remove("invoke_skill")
		p.runner.SetSkillInstructions("")
	}
	return nil
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func mcpServersFromIn(in httpapi.ToolConfigIn) ([]mcp.ServerSpec, error) {
	out := make([]mcp.ServerSpec, 0, len(in.MCPServers))
	for _, s := range in.MCPServers {
		out = append(out, mcp.ServerSpec{Name: s.Name, Command: s.Command, Args: s.Args})
	}
	return out, nil
}

func marshalJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func skillListing(l *skill.Loader) string {
	out := ""
	for _, m := range l.List() {
		out += fmt.Sprintf("- %s: %s\n", m.Name, m.Description)
	}
	return out
}
```
(Import the exact `httpapi` path as it is laid out in this repo — `internal/api/http` is imported elsewhere as `httpapi "rivus-agent-backend/internal/api/http"`; confirm the module path prefix from `go.mod`.)

- [ ] **Step 2: Wire `Start`** (`app.go`, replacing the discarded loader `Refresh()` at `app.go:79`):
  0. Add fields to the `App` struct (`app.go:34-39`): `manager *mcp.Manager` and `skillLoader *skill.Loader` (so `Stop` and the provider can reach them).
  1. `a.skillLoader = skill.NewLoader(cfg.SkillsDir)`; keep the value; `_ = a.skillLoader.Refresh()`.
  2. `a.manager = mcp.NewManager(mcp.NewStdioTransportFactory())`.
  3. `tc, _ := mcp.ReadToolConfig(ctx, settings)`; `a.manager.SpawnAll(ctx, tc.Servers)`; `registry.Replace("mcp__", mcp.MCPDefinitions(a.manager.Status(), a.manager))`.
  4. If `tc.SkillsEnabled`: `registry.Register(skill.InvokeSkill(a.skillLoader, 32*1024))` + `runner.SetSkillInstructions(skillListing(a.skillLoader))`.
  5. `provider := newToolConfigProvider(settings, a.manager, a.skillLoader, registry, runner)`.
  6. Pass the option into the server: `srv := httpapi.New(cfg, db, tasks, events, settings, runs, sessions, httpapi.WithToolConfigProvider(provider))`.

- [ ] **Step 3: Wire `Stop`** — `a.manager.StopAll()` before/with `a.srv.Shutdown(ctx)` (in `App.Stop`, `app.go:99`). Guard against a nil manager (the provider is optional).
- [ ] **Step 4: Sync note** — the registry mutation and `SetSkillInstructions` here can race an in-flight `EinoRunner.drain` that reads `registry.List()` live. Two guards are already in place from earlier tasks: registry mutations are `Registry.mu`-guarded (`registry.go:50`), and `SetSkillInstructions` is `EinoRunner.mu`-guarded (§6 delta a). A run in flight keeps the agent snapshot it built at start; reconcile only affects **new** agent builds. No extra lock is needed; document this invariant in `toolconfig.go`.
- [ ] **Step 5: Build + vet** — `cd backend && go build ./... && go vet ./internal/app/ ./internal/api/http/`.
- [ ] **Step 6: Smoke test** — `make run`, then `curl localhost:8080/api/v1/settings/tools` (expect the shape from §4.6, mcp `servers` empty or reflecting configured servers); `PUT` a `mcp.servers` value and confirm reconcile in the logs.
- [ ] **Step 7: Commit** — `git add backend/internal/app/app.go backend/internal/app/toolconfig.go && git commit -m "feat(app): wire MCP Manager + skill Loader + ToolConfigProvider + StopAll"`.

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
- Modify: `backend/tests/testutil/harness.go` (register `invoke_skill` + one canned `mcp__` tool def + `httpapi.WithToolConfigProvider`)
- Create: `backend/tests/integration/toolconfig_test.go`

The `tests/integration` package **cannot** import the `internal/mcp` `FakeTransport` unless the fake is exported — that's why Task 4 placed it in a **non-test `fake.go`** (`mcp.FakeTransport`/`mcp.NewFakeTransportFactory` are importable). For the integration harness, prefer the lightest path: register a **canned `mcp__` `tool.Definition`** directly (a stub `Handler` that returns a fixed string) so runs can call `mcp__fake__echo` without a live MCP process. If you want a *real* bridge end-to-end, use `mcp.NewManager(mcp.NewFakeTransportFactory())` + `mcp.MCPDefinitions` + `registry.Replace("mcp__", defs)` — same seam as Task 12.

- [ ] **Step 1: Wire harness** — in `harness.New`: register `skill.InvokeSkill(loader, 32*1024)` (point `loader` at a fixture `SKILL.md` dir); register a canned `mcp__fake__echo` def (`Risk=external, Approval=always`, `RequiredScopes` empty, stub handler); construct a minimal `ToolConfigProvider` stub and pass `httpapi.New(..., httpapi.WithToolConfigProvider(stub))` (the variadic option — existing 7-arg calls keep compiling).
- [ ] **Step 2: Write tests** — `backend/tests/integration/toolconfig_test.go`:
  - a run whose model calls the **pre-approved** `mcp__fake__echo` tool → `tool.completed` event + parent succeeds. `harness.CreateRun` always drives `rt.Execute` with an empty `ApprovedTools` map (`run_service.go:89`), so to exercise the pre-approved path drive the runner directly: `h.Runner.Run(ctx, runtime.RunRequest{RunID:…, OwnerID:…, Goal:…, ApprovedTools: map[string]bool{"mcp__fake__echo:" + itool.ArgsHash(rawArgs): true}})` where `rawArgs` is the exact argument JSON the scripted model emits. The unapproved path (bullet 2) goes through the public API.
  - a run whose model calls the `external/always` MCP tool → `waiting_approval` → `decide` (approve) → `resume` (mirror `approval_test.go`'s pattern).
  - `invoke_skill` returns the SKILL.md body as tool output.
  - `GET /settings/tools` shape; `PUT` persists + reconciles (assert the stub recorded the payload).
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
