# Extensible Agent Tooling — Skill + MCP + Generic Tool Config (Design)

Date: 2026-10-15
Status: Draft for spec review
Project: rivus-agent

## 1. Problem & Goal

Today the agent is effectively a **fixed-tool** runtime. The only tool surface is
`tool.Registry`, populated exclusively from `internal/tool/builtin`
(`knowledge_search`, `file_read`, `workspace_write`) plus the `task` subagent tool.
The `skill` package *loads* `SKILL.md` files but the loader result is discarded
(`app.go`: `_ = skill.NewLoader(cfg.SkillsDir).Refresh()`), so skills are inert.
MCP does not exist; the sole reference is a comment in `registry.go`
("Registry 是原生 Go Tool Registry（后续可选 MCP）").

This design makes the agent **general-purpose**: it can invoke **skills** and
**MCP** tools, driven by a **global tool configuration surface**, all funneling
into the one `tool.Registry` the agent already reads.

Success = the running agent can call an external MCP tool and read a skill's
instructions as context, both governed by the existing risk/approval/budget
pipeline, configured globally from the settings store + the frontend Settings
dialog, with no change to `EinoRunner`, `tool.Executor`, or the approval state
machine.

## 2. Locked Decisions (from brainstorm)

| Decision | Choice |
|---|---|
| Scope this round | All three — MCP, skill, generic tool config — in one spec, sequenced in chunks |
| MCP transport | stdio-first; pluggable `Transport` seam so HTTP/SSE is a later drop-in |
| Skill semantics | Context/instructions: `invoke_skill` returns the SKILL.md body to the model; no script execution |
| Config scope | Global (settings table), not per-session |
| MCP trust | Default-high-risk: every MCP tool registers `Risk=external, Approval=always` |
| MCP lifecycle | Start-time spawn + keep-alive; reconcile on config change; kill on shutdown |

## 3. Invariant

The agent reads **only** `*tool.Registry` and dispatches through
`tool.Executor`; every call is gated by `tool.CheckPolicy` on the
`Definition`'s `Risk`/`Approval` and bounded by the run budget. Therefore MCP and
skill tools become usable the moment they are bridged into that registry. No new
execution path is added to the runner.

## 4. Components

### 4.1 `internal/mcp` (new package)

```
internal/mcp/
├── transport.go   # Transport interface { Start() error; Write([]byte) error; ReadFrame() ([]byte, error); Stop() error }
│                  #   + stdioTransport (os/exec child, io.Pipe stdin/stdout, Setpgid for group kill)
├── rpc.go         # JSON-RPC 2.0: request ids, response matching, 1-message-per-line framing
├── client.go      # one MCP server: initialize → tools/list → tools/call; MCP result blocks → (string, error)
└── manager.go     # Manager owns all connections; SpawnAll(config), Reconcile(config), StopAll()
```

- **Framing**: MCP stdio carries one JSON-RPC message per line. `client.go`
  speaks `initialize`, `tools/list`, `tools/call`.
- **Result mapping**: `tools/call` returns a content block list; text blocks are
  joined into the handler `string`; `isError` results map to a Go `error` so the
  executor records `tool.failed`.
- **Process hygiene**: child in its own process group; `Stop` kills the group.
  A 5s deadline on `initialize`/`tools/list` so a broken server cannot hang agent
  start. Per-call timeout = the tool `Definition.TimeoutMs`.
- **Crash tolerance (v1)**: a dead server's `tools/call` returns
  `"mcp server <name> disconnected"`; the loop records `tool.failed` and
  continues. **No auto-respawn** — tracked as a follow-up.
- **Reconcile**: on a tool-config change, diff the server set — stop removed,
  spawn added, refresh the registry's MCP entries. Wired from the settings save
  path (same place model settings apply today).

### 4.2 `internal/skill` — `invoke_skill` tool (new tool, existing package)

- Handler: `Loader.Get(name)` → SKILL.md body, truncated to the tool's
  `MaxResultBytes`, returned as tool output. Unknown or disabled → structured
  error (agent sees `tool.failed`).
- `Risk=read, Approval=never`: it only hands text to the model.
- Available skill names + one-line descriptions are injected into the agent
  instruction (cheap), so the model knows what it can call without a separate
  discovery tool.
- `skills.enabled` gates whether `invoke_skill` is registered at all. The
  loader is now actually wired (the current discarded `Refresh()` result is used).

### 4.3 Registry assembly (at start / on reconcile)

```
registry.RegisterAll(builtin)              # existing
if skills.enabled: registry.Register(invoke_skill)
if mcp.enabled:  manager.SpawnAll(mcp.servers) → register mcp__<srv>__<tool> defs
```

Tool names are mangled to `mcp__<server>__<tool>` to avoid collision with
builtin/skill names. Every MCP def is `Risk=external, Approval=always`.

### 4.4 Config schema (settings table, global)

```
skills.enabled   "true" | "false"
mcp.enabled      "true" | "false"
mcp.servers      JSON array:
                 [{"name":"fs","command":"npx",
                   "args":["-y","@modelcontextprotocol/server-filesystem","/path"]}]
```

- `name` matches `^[a-z0-9_-]+$`, unique; `command` is on PATH (absolute
  recommended). A server that fails `initialize` is logged + skipped; the agent
  starts with the rest and reports it via a `tool.mcp_status` event.
- **Secrets (v1 limitation)**: env values in `args` are literal strings only;
  no secret-injection / placeholder expansion. Documented; cheap follow-up.

### 4.5 Frontend — Settings "Tools" panel

Extends `SettingsDialog` + settings API; no new routing. Two sub-panels:
- **Skills**: `skills.enabled` toggle + read-only list of discovered skills.
- **MCP**: `mcp.enabled` toggle + server list editor with a connect-status badge
  driven by `tool.mcp_status` events + per-server discovered-tool list.

## 5. Data Flow

```
config (settings) → manager.SpawnAll/Reconcile → tools/list → tool.Definition (external/always)
   → Registry → EinoRunner (unchanged) → model decides → Executor.CheckPolicy
   → approval gate (external/always ⇒ waiting_approval card, as today)
   → manager.Call → child process → result → tool.completed/failed → budget counts it
```

Skill path: model calls `invoke_skill` → `Loader.Get` → SKILL.md text as tool
output → model continues with it as context.

## 6. Error Handling & Security

- **Approval**: MCP calls flow the existing `CheckPolicy` pipeline;
  `Risk=external + Approval=always` → `waiting_approval` UI card, identical to
  `workspace_write` today. Approval binding (args hash + expiry) is reused.
- **Failures**: spawn/init failure → server skipped, logged + `tool.mcp_status`;
  mid-run disconnect → clean `tool.failed` tool error, loop continues; shutdown
  → `StopAll` kills process groups.
- **Boundedness**: every MCP call is a run tool call — subject to
  `MaxToolCalls` / `MaxModelCalls` / duration budgets and `MaxResultBytes`
  truncation exactly like any builtin tool.
- **Single-instance**: consistent with the existing SQLite single-process model;
  process-group kill on shutdown prevents orphaned children.

## 7. Testing

- **`internal/mcp`**: in-process fake MCP stdio server (canned
  `initialize`/`tools/list`/`tools/call`) drives client + manager tests,
  including the crash and reconcile paths.
- **`invoke_skill`**: loader-backed test against a fixture `SKILL.md`.
- **Registry assembly**: assert builtin + skill + MCP coexist and mangled names
  don't collide.
- **Integration (testutil harness)**: one fake MCP server → a run produces
  `tool.completed`; a high-risk MCP tool → `waiting_approval` → decide →
  resume.
- **Regression**: full suite + `go vet ./...`; acceptance gates stay
  Go-only (no frontend diff in the Go checks).

## 8. Sequenced Chunks

1. Settings keys (`skills.enabled`, `mcp.enabled`, `mcp.servers`) + registry
   assembly seam.
2. `internal/mcp` (transport → client → manager) + registry bridge.
3. `invoke_skill` tool (wire the loader, inject names into instruction).
4. Settings "Tools" UI + `tool.mcp_status` event.
5. Integration tests + docs (README tool-config section).

## 9. Explicit Non-Goals (v1)

- Auto-respawn of crashed MCP servers (follow-up).
- HTTP/SSE MCP transport (seam exists; not built).
- Per-session tool scoping; secret placeholder expansion; skill script execution.
