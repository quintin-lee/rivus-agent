# Rivus Agent

A lightweight AI Agent service built on **Go + CloudWeGo Eino ADK**. Single process, SQLite, bounded Workers — general-purpose Agent capabilities with planning, tool calling, pause/resume, and full auditability.

## Features

- **Dual execution modes**: ReAct tool loop by default; switch to Plan-Execute for complex tasks
- **Human approval**: high-risk tool calls enter `waiting_approval` state without occupying a Worker; resume after approval
- **Interrupt recovery**: SQLite Checkpoints persist Eino execution state; orphan Runs are scanned automatically at startup
- **Budget control**: per-Run caps on duration, model calls, tool calls, iterations, and output size
- **Security governance**: tiered tool permissions (read / write / external side-effect / denied), approval-bound arguments, sensitive-field redaction
- **SSE event stream**: replay after disconnect, resume via `Last-Event-ID`

## Project Structure

```text
rivus-agent/
├── backend/                  # Go service
│   ├── cmd/agentd/           # entrypoint
│   ├── internal/
│   │   ├── app/              # startup wiring, graceful shutdown
│   │   ├── config/           # env vars + YAML config loading
│   │   ├── api/http/         # REST + SSE + auth
│   │   ├── domain/           # state machine, error classification
│   │   ├── service/          # RunService / SessionService
│   │   ├── runtime/          # Eino Runner, Planner, Verifier, Budget, Recovery
│   │   ├── model/            # OpenAI-compatible ModelFactory, retry classification
│   │   ├── tool/             # Registry, unified execution gateway, permission policy, approval
│   │   ├── skill/            # on-demand Skill loading and validation
│   │   ├── store/            # SQLite repositories (Task/Event/Approval/Checkpoint)
│   │   ├── observability/    # slog logging, expvar metrics
│   │   └── security/         # redaction, secrets, tenant ownership
│   ├── configs/              # config examples
│   ├── skills/               # local Skill directory
│   ├── tests/                # integration tests
│   └── Makefile
├── frontend/                 # Frontend (React + Vite + TS + Tailwind, 127 tests)
├── docs/                     # Design docs (superpowers/specs, plans)
├── docker-compose.yml        # One-command start (backend + frontend)
└── .env.example              # Environment variable example
```

## Quick Start

**Requirements**: Go 1.27+, no CGO needed.

```bash
# Enter backend
cd backend

# Build
make build

# Configure the model (environment variables)
export MODEL_BASE_URL=https://api.openai.com/v1
export MODEL_API_KEY=sk-xxx
export MODEL_NAME=gpt-4o-mini

# Optional: create a local config
cp configs/config.example.yaml configs/config.yaml

# Start
./bin/agentd -config configs/config.yaml
```

Verify the service:

```bash
curl localhost:8080/healthz   # liveness
curl localhost:8080/readyz    # readiness
curl localhost:8080/metrics   # expvar metrics
```

Create a session and submit a task:

```bash
# Create session
curl -X POST localhost:8080/api/v1/sessions -d '{"title":"demo"}'

# Submit a Run
curl -X POST localhost:8080/api/v1/runs \
  -H 'Idempotency-Key: demo-1' \
  -d '{
    "session_id": "ses_xxx",
    "goal": "Analyze the project and propose refactoring suggestions",
    "mode": "plan_execute",
    "success_criteria": ["Produce a risk list", "Every suggestion backed by evidence"],
    "budget": {
      "max_duration_seconds": 600,
      "max_model_calls": 30,
      "max_tool_calls": 50,
      "max_iterations": 12
    }
  }'

# Subscribe to the event stream (SSE, supports Last-Event-ID replay)
curl -N localhost:8080/api/v1/runs/<run_id>/events
```

## Docker Quick Start

```bash
cp .env.example .env        # fill in MODEL_BASE_URL / MODEL_API_KEY / MODEL_NAME
docker compose up -d
```

- Backend API: `http://localhost:8080` (healthcheck probes via `wget /healthz`;
  the frontend container uses `depends_on: service_healthy` and starts once ready)
- Frontend UI: `http://localhost:8081` (nginx static hosting, reverse-proxies
  `/api`, `/healthz`, `/readyz`, `/metrics` to the backend container)
- Data persistence: volume `rivus-data` → `/data/agent.db` in the container
  (SQLite, WAL mode); the backend skills directory is mounted read-only from
  `./backend/skills`
- Registry mirror: set `DOCKER_REGISTRY=registry.1ms.run/` (or similar prefix) in `.env`

## API Overview

| Method | Path | Purpose |
|---|---|---|
| POST | `/api/v1/sessions` | Create session |
| GET | `/api/v1/sessions` | List sessions |
| GET | `/api/v1/sessions/{id}` | Get session |
| GET | `/api/v1/sessions/{id}/memories` | List session memories |
| DELETE | `/api/v1/sessions/{id}` | Delete session |
| GET | `/api/v1/runs` | List runs |
| POST | `/api/v1/runs` | Submit task (supports `Idempotency-Key`) |
| GET | `/api/v1/runs/{id}` | View run status and result |
| GET | `/api/v1/runs/{id}/events` | SSE event stream (`Last-Event-ID` replay) |
| POST | `/api/v1/runs/{id}/cancel` | Cancel run |
| POST | `/api/v1/runs/{id}/resume` | Resume paused / interrupted task |
| POST | `/api/v1/runs/{id}/approvals/{approval_id}` | Approve / reject |
| GET | `/api/v1/runs/{id}/approvals` | List approvals |
| GET | `/api/v1/settings/tools` | Read Skills/MCP tool configuration |
| PUT | `/api/v1/settings/tools` | Update Skills/MCP tool configuration |
| GET | `/api/v1/settings/model` | Read model settings |
| PUT | `/api/v1/settings/model` | Update model settings |
| GET | `/healthz` | Liveness check |
| GET | `/readyz` | Readiness check |
| GET | `/metrics` | expvar metrics (includes latency histogram buckets) |

## Configuration

| Env var / config key | Description |
|---|---|
| `AGENT_CONFIG` / `-config` | Path to YAML config file (optional) |
| `AGENT_ADDR` | Listen address, default `:8080` |
| `AGENT_DB_PATH` | SQLite file path, default `./data/agent.db` |
| `AGENT_AUTH_TOKEN` | API Bearer Token; empty means no auth (local development only) |
| `MODEL_BASE_URL` | OpenAI-compatible gateway URL |
| `MODEL_NAME` | Model name |
| `MODEL_API_KEY` | API key |
| `WORKER_CONCURRENCY` | Number of concurrent workers, default 1 |
| `MODEL_PROVIDER` | Model provider identifier; defaults to the OpenAI-compatible protocol |
| `AGENT_SKILLS_DIR` | Local Skill directory, default `./skills` |
| `AGENT_WORKSPACE_DIR` | Tool workspace root (read/write scope of file_list/file_search/file_edit/workspace_write), default `./workspace` |
| `AGENT_KNOWLEDGE_DIR` | Knowledge directory (search scope of knowledge_search), default `./knowledge` |
| `AGENT_HTTP_FETCH_ALLOWLIST` | http_fetch domain allowlist (comma-separated, exact match; empty = outbound fetch fully disabled) |
| `AGENT_RATE_LIMIT_RPM` | API rate limit (requests per minute per client, keyed by X-Owner-ID or source IP; 0 = disabled, default 0) |
| `LOG_LEVEL` | Log level `debug/info/warn/error`, default `info` |

See `backend/configs/config.example.yaml` for a YAML config example.

Budget limits requested by clients are only ever tightened by server-side policy, never relaxed.

## Common Commands

```bash
make build   # build to bin/agentd (with -trimpath)
make test    # full unit test suite
make vet     # go vet
make run     # local start (export the three model vars first)
make clean   # remove build artifacts
```

## Tech Stack

| Layer | Choice |
|---|---|
| Language | Go 1.27+, CGO-free |
| Agent framework | CloudWeGo Eino ADK `v0.9.21` |
| Persistence | SQLite (`modernc.org/sqlite`), WAL mode |
| Model adapter | OpenAI-compatible Chat Completions |
| Logging | `log/slog` structured logs |
| Metrics | `expvar` (built-in) |

## Security Notes

- Secrets only via environment variables — never written to the repo or the prompt
- Tool permissions are re-validated on every call, never cached
- High-risk side-effect operations require human approval by default; approvals bind an argument digest + expiry
- Sensitive fields are auto-redacted in logs / events / error messages
- Single-instance deployment: mount the SQLite file on a persistent volume and back it up regularly

## Further Documentation

- Architecture: [`docs/en/architecture/`](./docs/en/architecture/) (component diagram, run lifecycle, state machine)
- API reference: [`docs/en/api/`](./docs/en/api/) (per-endpoint + SSE event catalog)
- Design docs (specs / plans): [`docs/superpowers/`](./docs/superpowers/)
