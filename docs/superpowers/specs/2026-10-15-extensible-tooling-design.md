# Extensible Agent Tooling — Skill + MCP + Global Tool Config (Design)

Date: 2026-10-15
Status: Draft for spec review (rev 2, post reviewer pass 1)
Project: rivus-agent

## 1. Problem & Goal

Today the agent is effectively a **fixed-tool** runtime. The only tool surface is
`tool.Registry`, populated exclusively from `internal/tool/builtin`
(`knowledge_search`, `file_read`, `workspace_write`) plus the `task` subagent
tool. The `skill` package *loads* `SKILL.md` files but the loader result is
discarded (`app.go`: `_ = skill.NewLoader(cfg.SkillsDir).Refresh()`), so skills
are inert. MCP does not exist; the sole reference is a comment in `registry.go`
("Registry 是原生 Go Tool Registry（后续可选 MCP）").

This design makes the agent **general-purpose**: it can invoke **skills** and
**MCP** tools, driven by a **global tool configuration surface**, all funneling
into the one `tool.Registry` the agent already reads.

Success = the running agent can call an external MCP tool and read a skill's
instructions as context, both governed by the existing risk/approval/budget
pipeline, configured globally from the settings store + the frontend Settings
dialog.

**Bounded runner deltas only** (see §6): the runner gains two small, enumerable
changes (skill-name instruction augmentation + an `EvtToolFailed` emission).
There is **no new tool *execution* path** in the runner — MCP and skill tools run
through the existing `tool.Executor` + `CheckPolicy` + approval + budget pipeline.

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

The agent reads **only** `*tool.Registry` (held by `EinoRunner` by pointer,
`runner_eino.go:52`) and dispatches through `*tool.Executor`; every call is gated
by `tool.CheckPolicy` on the `Definition`'s `Risk`/`Approval` and bounded by the
run budget. Because the runner holds the pointers, MCP and skill tools become
usable the moment they are bridged into that registry — **in-place registry
mutation** (add/remove entries on the shared `*Registry`) keeps the runner valid
with no re-threading. No new execution path is added to the runner; only the two
bounded deltas in §6.

## 4. Components

### 4.1 `internal/mcp` (new package)

```
internal/mcp/
├── transport.go   # Transport interface { Start() error; Write([]byte) error; ReadFrame() ([]byte, error); Stop() error }
│                  #   + stdioTransport (os/exec child, io.Pipe stdin/stdout, Setpgid for group kill)
├── rpc.go         # JSON-RPC 2.0: request ids, response matching, one-JSON-message-per-line framing
├── client.go      # one MCP server: initialize → tools/list → tools/call; MCP content blocks → (string, error)
└── manager.go     # Manager owns all connections; SpawnAll(cfg), Reconcile(cfg), StopAll(); exposes Status()
```

- **Framing**: MCP stdio carries one JSON-RPC message per line. `client.go`
  speaks `initialize`, `tools/list`, `tools/call`.
- **Result mapping**: `tools/call` returns a content-block list; text blocks are
  joined into the handler `string`; an `isError` result maps to a Go `error`.
- **Process hygiene**: child in its own process group (`Setpgid`); `Stop` kills
  the group. A 5s deadline on `initialize`/`tools/list` so a broken server cannot
  hang agent start.
- **Timeout (v1)**: MCP `tools/list` carries no per-tool timeout, so every
  `mcp__` `Definition` uses the registry default (`TimeoutMs=0 → 30000`,
  `registry.go:61-63`). Per-server / per-tool timeouts are a **non-goal (§9)**.
- **Crash tolerance (v1)**: a dead server's `tools/call` returns
  `"mcp server <name> disconnected"`. The failing call fails the run through the
  existing error path (§6 EvtToolFailed); **no auto-respawn** — a follow-up (§9).
- **Reconcile (registry mutation seam)**: `Registry` exposes only
  `Register`/`Get`/`List` today and `Register` **rejects duplicates**
  (`registry.go:57-73`) — it has **no delete/replace API**. So reconcile must
  mutate the shared registry in place. **Add two small, testable methods to
  `tool.Registry`:** `RemoveByPrefix(prefix string) int` and
  `Replace(prefix string, defs []Definition)` (clear prefix, re-add). `Manager`
  drives them on config change: stop removed servers → `RemoveByPrefix("mcp__<name>__")`
  → spawn added servers → `Replace("mcp__", allCurrentMCPDefs)`. This keeps the
  runner (which holds the `*Registry` pointer) fully valid with **zero runner
  re-wiring**.

### 4.2 `internal/skill` — `invoke_skill` tool (new tool, existing package)

- Handler: `Loader.Get(name)` → SKILL.md body, truncated to the tool's
  `MaxResultBytes`, returned as tool output. **Unknown skill name → structured
  error** (agent sees a tool error). There is **no per-skill enable flag** in
  v1: `skills.enabled` (global) gates whether the tool is *registered at all*;
  when on, every loaded skill is callable by name. (A per-skill flag is a
  follow-up, not here.)
- `Risk=read, Approval=never`: it only hands text to the model.
- **RequiredScopes stays empty** (§4.3 invariant) — same as `invoke_skill`
  must not carry scopes.
- Available skill names + one-line descriptions are injected into the agent
  instruction (§6 delta a) so the model knows what it can call.

### 4.3 Registry assembly (at start / on reconcile)

```
registry.RegisterAll(builtin)               # existing
if skills.enabled: registry.Register(invoke_skill)
if mcp.enabled:  manager.SpawnAll(cfg) → registry.Replace("mcp__", defs)
```

- Tool names are mangled to `mcp__<server>__<tool>` to avoid collision with
  builtin/skill names. Every MCP def is `Risk=external, Approval=always`.
- **RequiredScopes invariant (critical):** `gatewayTool` builds
  `ExecRequest` with **no `Scopes`** (`runner_eino.go:145-155`), so
  `CheckPolicy` evaluates with `nil` scopes and **any `Definition` carrying a
  non-empty `RequiredScopes` is hard-DENIED** ("missing scope", never routed to
  approval — this is already true for `workspace_write`). Therefore `mcp__` and
  `invoke_skill` defs **MUST leave `RequiredScopes` empty**. The bridge must
  **not** map any MCP server "permissions" to `RequiredScopes` (it would silently
  produce `tool.denied` instead of the promised `waiting_approval` card).

### 4.4 Config schema (settings table, global)

```
skills.enabled   "true" | "false"
mcp.enabled      "true" | "false"
mcp.servers      JSON array:
                 [{"name":"fs","command":"npx",
                   "args":["-y","@modelcontextprotocol/server-filesystem","/path"]}]
```

- `name` matches `^[a-z0-9_-]+$`, unique; `command` is on PATH (absolute
  recommended). A server that fails `initialize` is logged + skipped (agent
  starts with the rest); its status is surfaced via §4.6, not an event.
- **Secrets (v1 limitation)**: env values in `args` are literal strings only;
  no secret-injection / placeholder expansion. Documented; cheap follow-up (§9).

### 4.5 Frontend — Settings "Tools" panel

Extends `SettingsDialog` + the §4.6 API; no new routing. Two sub-panels:
- **Skills**: `skills.enabled` toggle + read-only list of available skills.
- **MCP**: `mcp.enabled` toggle + server list editor, a **connect-status badge
  per server**, and a per-server discovered-tool list.

**Status source (revised):** the badge is driven by a **REST fetch**, not a
per-run SSE event. Rationale: `agent_events.run_id` is `NOT NULL` with an FK to
`runs` (`schema.sql`), so a *server-level* status has no run to attach to; and
the frontend's only SSE client subscribes to `/api/v1/runs/{id}/events`
(`sse-client.ts`), which requires an active run. So the dialog reads
`GET /api/v1/settings/tools` on open and after each save; no live monitor in v1
(a reconnect/poll loop is a follow-up).

### 4.6 Settings/tools REST API + wiring (NEW, closes the "no home" gap)

Two endpoints (mirror the existing `GET/PUT /api/v1/settings/model`,
`server.go:60-61`):

- `GET /api/v1/settings/tools` →
  `{ "skills": {"enabled": bool, "available": [{"name","description"}]},
     "mcp":    {"enabled": bool,
                 "servers": [{"name","status":"up|down|error","command","tools":[{"name","description"}]}] } }`
- `PUT /api/v1/settings/tools` →
  body `{ "skillsEnabled": bool, "mcpEnabled": bool,
          "mcpServers": [{"name","command","args":[]}] }`; persists the three
  settings keys then **triggers `Manager.Reconcile`** (the same save path where
  model settings are applied today).

**Wiring to the server layer:** `app.go` constructs the `*mcp.Manager` (holding
its child connections) and the `*skill.Loader`. `httpapi.New` gains a small
interface param, e.g.
```
type ToolConfigProvider interface {
    GetToolsStatus(ctx context.Context) (ToolConfig, error)   // reads settings + live manager.Status()
    SaveTools(ctx context.Context, in ToolConfig) error        // persists + manager.Reconcile
}
```
implemented in `internal/app` (which owns the `SettingsRepo`, the `Manager`, and
the `Loader`). This is the seam the reviewer flagged ("how do the MCP Manager +
skill Loader reach the server layer") — solved by App implementing the provider
and passing it into `httpapi.New`, keeping the HTTP layer decoupled from both.

## 5. Data Flow

```
config (settings/tools PUT) → persist keys → Manager.Reconcile
   → stop removed / spawn added / registry.Replace("mcp__", defs)  [in-place, runner untouched]
   → tools/list → tool.Definition (external/always, empty RequiredScopes)
   → EinoRunner (existing) → model decides → Executor.CheckPolicy
   → approval gate (external/always ⇒ waiting_approval card, as today)
   → Manager.Call → child process → result
       ok      → tool.completed → budget counts it
       error   → EvtToolFailed (§6) → run fails via existing path
```

Skill path: model calls `invoke_skill` → `Loader.Get` → SKILL.md text as tool
output → model continues with it as context.

## 6. Error Handling & Security + Bounded Runner Deltas

**Two and only two `EinoRunner` deltas** (enumerated; no new execution path):

- **(a) Skill-name instruction augmentation.** `instruction` is a hardcoded field
  set at construction (`runner_eino.go:55,75`) and consumed at agent build
  (`runner_child.go:105`). Add a setter / builder hook so the assembled instruction
  appends the available skill names + one-line descriptions (empty when no
  skills). Bounded: one field + one call.
- **(b) `EvtToolFailed` emission.** `EvtToolFailed` is declared in the domain
  (`domain/event.go:25`) but emitted nowhere; `drain` handles tool errors only
  via the run-level `EvtModelFailed`/`EvtRunFailed` path. Add one `EvtToolFailed`
  emission in `drain` when a tool error occurs (observability/audit), **keeping
  the existing run-fail behavior** for a failed MCP call. Making failing tool
  calls *non-fatal* / auto-retried is a **non-goal (§9)**.

Everything else is unchanged:
- **Approval**: `external + always` → `waiting_approval` card, identical to
  `workspace_write`; approval binding (args hash + expiry) reused.
- **Boundedness**: every MCP call is a run tool call — subject to
  `MaxToolCalls`/`MaxModelCalls`/duration budgets and `MaxResultBytes`
  truncation exactly like any builtin tool.
- **Single-instance**: process-group kill on shutdown prevents orphaned
  children, consistent with the SQLite single-process model.

## 7. Testing

- **`tool.Registry` mutation**: `RemoveByPrefix` + `Replace` unit tests
  (add/remove/re-add `mcp__` entries; assert no dup-register error, assert
  `List` reflects the change on the *same* pointer).
- **`internal/mcp`**: in-process fake MCP stdio server (canned
  `initialize`/`tools/list`/`tools/call`) drives client + manager tests,
  including the crash path and the reconcile (add/remove/replace) path.
- **`invoke_skill`**: loader-backed test against a fixture `SKILL.md`; unknown
  name → error; `skills.enabled=false` → tool not registered.
- **Registry assembly**: assert builtin + skill + MCP coexist; mangled names
  don't collide; every `mcp__` def has `RequiredScopes` empty +
  `external/always`.
- **`/settings/tools`**: GET shape matches §4.6; PUT persists keys + calls
  Reconcile (assert manager status reflects a spawned fake server).
- **Runner deltas**: (a) instruction includes available skill names;
  (b) `drain` emits `EvtToolFailed` on a forced tool error.
- **Integration (testutil harness)**: one fake MCP server → a run produces
  `tool.completed`; a high-risk MCP tool → `waiting_approval` → decide →
  resume.
- **Regression**: full suite + `go vet ./...`; Go acceptance gates stay
  Go-only (no frontend diff in the Go checks).

## 8. Sequenced Chunks

1. `tool.Registry` mutation methods (`RemoveByPrefix`/`Replace`) + settings keys
   (`skills.enabled`, `mcp.enabled`, `mcp.servers`) + `/settings/tools` API +
   `ToolConfigProvider` wiring.
2. `internal/mcp` (transport → client → manager) + registry bridge (`Replace`).
3. `invoke_skill` tool (wire the loader) + runner delta (a) instruction +
   (b) `EvtToolFailed`.
4. Settings "Tools" UI (skills + MCP badges via the GET endpoint).
5. Integration tests + docs (README tool-config section).

## 9. Explicit Non-Goals (v1)

- Auto-respawn of crashed MCP servers.
- Non-fatal / auto-retried failing tool calls (v1 fails the run on a tool error).
- HTTP/SSE MCP transport (the `Transport` seam exists; not built).
- Per-session tool scoping; per-skill enable flags; secret placeholder
  expansion; per-server / per-tool timeouts; skill script execution.
