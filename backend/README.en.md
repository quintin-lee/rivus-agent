# Rivus Agent Backend

Go + CloudWeGo Eino ADK lightweight AI Agent service: single process, SQLite, bounded workers.
Design docs are in [`../docs/superpowers/`](../docs/superpowers/); for the repository overview see [`../README.en.md`](../README.en.md).

## Directory Layout

```text
cmd/agentd            # service entrypoint
internal/
  app/                # startup wiring, graceful shutdown
  config/             # env var + YAML config loading and validation
  api/http/           # REST, SSE, auth middleware
  domain/             # Task/Run/Step/Approval/Result, state machine, error classification
  service/            # RunService (create/cancel/approve/resume), SessionService
  runtime/            # execution orchestration, Eino Runner wrapper, Planner, Verifier, Budget, Recovery
  model/              # in-house OpenAI-compatible ChatModel, ModelFactory, retry classification
  tool/               # Registry, unified execution gateway, permission policy, approval binding
  tool/builtin/       # built-in read-only tools + controlled write tools
  skill/              # on-demand Skill manifest loading and validation
  store/              # SQLite init/migrations, Task/Event/Approval/Checkpoint repositories
  observability/      # slog logging, expvar metrics
  security/           # redaction, secret reading, tenant ownership checks
configs/              # config examples
skills/               # local Skill directory
```

## Quick Start

```bash
make tidy && make build
cp configs/config.example.yaml configs/config.yaml  # optional
MODEL_BASE_URL=https://api.openai.com/v1 MODEL_API_KEY=sk-xxx MODEL_NAME=gpt-4o-mini \
  ./bin/agentd -config configs/config.yaml
```

Liveness: `curl localhost:8080/healthz`, readiness: `/readyz`, metrics: `/metrics`.

Create a session and submit a task:

```bash
curl -X POST localhost:8080/api/v1/sessions -d '{"title":"demo"}'
curl -X POST localhost:8080/api/v1/runs \
  -H 'Idempotency-Key: demo-1' \
  -d '{"session_id":"ses_xxx","goal":"Analyze the project and propose refactoring suggestions","mode":"plan_execute",
       "success_criteria":["Produce a risk list","Every suggestion backed by evidence"],
       "budget":{"max_duration_seconds":600,"max_model_calls":30,"max_tool_calls":50,"max_iterations":12}}'
```

Event stream: `GET /api/v1/runs/{id}/events` (SSE, supports replay after disconnect).

## Configuration

Secrets travel only via environment variables — never into the repo or the prompt:

| Env var | Description |
|---|---|
| `AGENT_CONFIG` | YAML path (optional; can also be given via `-config`) |
| `AGENT_ADDR` | Listen address, default `:8080` |
| `AGENT_DB_PATH` | SQLite file path |
| `AGENT_AUTH_TOKEN` | API Bearer Token; empty means no auth (local development only) |
| `MODEL_BASE_URL` / `MODEL_NAME` / `MODEL_API_KEY` | OpenAI-compatible gateway trio |
| `WORKER_CONCURRENCY` | Number of concurrent workers, default 1; raise after load testing |

Budgets requested by clients are only ever tightened by server-side caps, never relaxed.

## Common Commands

```bash
make build             # build to bin/agentd (with -trimpath; production can add -tags prod with -ldflags="-s -w")
make test              # full test suite (unit + integration + regression)
make test-integration  # API/approval-chain integration tests only
make test-eval         # regression evaluation scenarios only
make bench             # benchmarks + concurrency smoke
make vet               # go vet
make run               # local start (export the model trio first)
make clean             # remove build artifacts
```

## Tool Configuration (Skills + MCP)

Built-in tools (`tool/builtin/`, registered at startup): `knowledge_search` (search the knowledge directory, read-only), `file_read` (read workspace files, read-only), `file_list` (list directories, read-only), `file_search` (full-text search across the workspace, read-only), `file_edit` (exact-unique replacement, approval by default), `workspace_write` (write drafts, approval by default), `http_fetch` (allowlisted outbound GET, approval always). Directories and the allowlist are configured via `AGENT_WORKSPACE_DIR` / `AGENT_KNOWLEDGE_DIR` / `AGENT_HTTP_FETCH_ALLOWLIST`.
Child-agent delegation tools (`internal/subagent`, registered at startup): `task` (synchronously delegate a single read-only child agent), `tasks` (batch parallel delegation, default concurrency 4 / request cap 8 / batch size 1..5, results returned in input order, child budget = 40% of parent, tightened only).
Session memory tools (`tool/builtin/memory.go`, registered at startup): `memory_write` (write a session memory, ≤2KB each, LRU keeps the latest 100 per session, cascade-deleted with the session), `memory_read` (read memories, optional substring filter, case-insensitive). The latest 10 (≤8KB) are auto-injected into top-level run instructions; query endpoint `GET /api/v1/sessions/{id}/memories`.

Three global `server_settings` keys (key/value, no migrations):

| Key | Value | Description |
|---|---|---|
| `skills.enabled` | `"true"/"false"` | Whether to register the `invoke_skill` tool |
| `mcp.enabled` | `"true"/"false"` | Whether to start the MCP server + expose `mcp__` tools |
| `mcp.servers` | JSON array | List of stdio MCP servers (see below) |

`mcp.servers` example (stdio; child processes run in their own process group, reclaimed as a group on `StopAll`):

```json
[{"name":"fs","command":"npx","args":["-y","@modelcontextprotocol/server-filesystem","/tmp"]}]
```

API: `GET /api/v1/settings/tools` (read config + live status and discovered tools per server);
`PUT /api/v1/settings/tools` (persist the three keys + trigger `Manager.Reconcile` + in-place
refresh of the shared `Registry`'s `mcp__` family; running Agent snapshots are unaffected,
only new Runs pick up changes).

**Security invariants (§4.3)**: `tool.Definition`s for `mcp__` and `invoke_skill` always leave
`RequiredScopes` empty (the gateway passes nil scopes; `CheckPolicy` hard-rejects any non-empty
scope). MCP tools are **high-risk external + approval always** by default (same as
`workspace_write`, going through the `waiting_approval` card); `invoke_skill` is
**read-only + approval never**, handing the SKILL.md body to the model as context.

v1 non-goals: auto-reconnect, non-fatal tool calls, HTTP/SSE transport, per-session/per-skill toggles, secret expansion, per-tool timeouts.

## Evals

`tests/evals/` provides deterministic mock-model evaluation: 15 scenarios (8 tool-level + 7 agent-level) scored across three dimensions — **correctness / efficiency / stability** — and writes `eval-report.json` (pass rate + per-category summary, gitignored; uploaded as a CI artifact by the `evals` job).

```bash
cd backend && make test-eval   # equivalent to go test ./tests/evals/ -count=1
```

Adding a scenario: append `Run(t, Scenario{...})` in `scenarios_tool_test.go` / `scenarios_agent_test.go` and it is picked up automatically (framework in `framework.go`: `Expect` declares three-dimension expectations, `Script` scripts the mock model, `MidRun`/`PostCheck` handle approvals and extra assertions).

## Test Layout

```text
tests/
  testutil/     # scriptable fake model + Harness (in-memory SQLite + httptest, zero external deps)
  integration/  # full API chain: sessions/idempotency/SSE/cancel/approval approve & reject
  evals/        # regression scenarios: evidence-based success, no-evidence failure, Plan steps, budget must terminate
  bench/        # benchmarks (event writes / tool execution / run creation) + 20-concurrency smoke
  fixtures/     # sample request payloads
```

## Notes

- Eino is pinned to stable `v0.9.21`, no alphas; dependency upgrades must pass tool calling, streaming, and interrupt-recovery regressions.
- SQLite driver is `modernc.org/sqlite` (CGO-free); single-instance deployment — keep the DB file on a persistent volume and back it up regularly.
- High-risk tool calls go to human approval by default (`waiting_approval`, no Worker occupied) and resume after approval.
