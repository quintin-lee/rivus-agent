# API Reference · Settings

Runtime settings: Skills/MCP tool switches and model configuration. All endpoints are subject to auth and `X-Owner-ID` ownership (see [`overview.md`](overview.md)).

## GET /api/v1/settings/tools

Read Skills/MCP tool configuration plus live status per MCP server.

**Response** `200`:

| Field | Type | Description |
|---|---|---|
| `skills.enabled` | bool | Whether the `invoke_skill` tool is registered |
| `skills.available` | []object | Loaded skills: `{name, description}` |
| `mcp.enabled` | bool | Whether the MCP server starts and exposes `mcp__` tools |
| `mcp.servers` | []object | Per server: `{name, status, command?, args?, type?, url?, oauth?, tools}` |
| `mcp.servers[].status` | string | `up` / `down` / `error` |
| `mcp.servers[].oauth` | object | OAuth echo: `{token_url, client_id, scopes?, has_secret}` — **client_secret is never echoed** (same rule as headers) |
| `mcp.servers[].tools` | []object | Discovered tools: `{name, description}` |

**Errors**: `501 tools_not_configured` (no Tools Provider configured server-side).

```bash
curl localhost:8080/api/v1/settings/tools
```

## PUT /api/v1/settings/tools

Persist the tool configuration and trigger a hot update (`Manager.Reconcile`): the `mcp__` tool family is refreshed in place into the shared Registry. **Running Runs are unaffected; only new Runs pick up changes.**

| Request field | Type | Required | Description |
|---|---|---|---|
| `skillsEnabled` | bool | yes | Enable the Skill tool |
| `mcpEnabled` | bool | yes | Enable MCP |
| `mcpServers` | []object | no | MCP server list: stdio `{name, command, args?}` or http `{name, type:"http", url, headers?, oauth?}` |
| `mcpServers[].oauth` | object | no | Only for `type:"http"`: `{token_url, client_id, client_secret, scopes?}` (client-credentials; all three required, 400 otherwise) |

**Response** `200`: `{}`

**Errors**: `501 tools_not_configured` / `400 bad_request` / `500 internal`.

```bash
curl -X PUT localhost:8080/api/v1/settings/tools \
  -d '{"skillsEnabled":true,"mcpEnabled":true,"mcpServers":[{"name":"fs","command":"npx","args":["-y","@modelcontextprotocol/server-filesystem","/tmp"]}]}'

With OAuth for an http-type server:

```bash
curl -X PUT localhost:8080/api/v1/settings/tools \
  -d '{"skillsEnabled":true,"mcpEnabled":true,"mcpServers":[{"name":"remote","type":"http","url":"https://mcp.example.com/mcp","oauth":{"token_url":"https://auth.example.com/token","client_id":"rivus","client_secret":"***","scopes":["mcp:read"]}}]}'
```
```

## GET /api/v1/settings/model

Read the effective model settings (env vars layered with server-side stored values).

**Response** `200`:

| Field | Type | Description |
|---|---|---|
| `provider` | string | Model provider identifier (`openai_compat`) |
| `base_url` | string | OpenAI-compatible gateway URL |
| `model` | string | Model name |
| `api_key_set` | bool | Whether an API key is set (**the key itself is never returned**) |

```bash
curl localhost:8080/api/v1/settings/model
```

## PUT /api/v1/settings/model

Update model settings. **Empty-string fields mean no change**; `api_key` is write-only.

| Request field | Type | Required | Description |
|---|---|---|---|
| `provider` | string | no | Only `openai_compat` accepted; anything else returns 400 |
| `base_url` | string | no | Gateway URL |
| `model` | string | no | Model name |
| `api_key` | string | no | API key (after writing, the response only reflects `api_key_set: true`) |

**Response** `200`: same shape as GET (the updated effective values).

**Errors**: `400 bad_request` `"unsupported provider"` / `500 internal`.

```bash
curl -X PUT localhost:8080/api/v1/settings/model \
  -d '{"model":"gpt-4o-mini","base_url":"https://api.openai.com/v1","api_key":"sk-xxx"}'
```

## Notes

- The settings/tools PUT is a global configuration (not per-owner read/write), though reads still require auth
- MCP child processes run in their own process group, reclaimed as a group on graceful shutdown
- Model setting changes apply to the **next new Run**; currently executing Runs do not switch
