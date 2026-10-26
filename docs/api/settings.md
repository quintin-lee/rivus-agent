# API 参考 · 设置（Settings）

运行时设置：Skills/MCP 工具开关与模型配置。全部端点受认证与 `X-Owner-ID` 归属约束（见 [`overview.md`](overview.md)）。

## GET /api/v1/settings/tools

读取 Skills/MCP 工具配置及各 MCP 服务器实时状态。

**响应** `200`：

| 字段 | 类型 | 说明 |
|---|---|---|
| `skills.enabled` | bool | 是否注册 `invoke_skill` 工具 |
| `skills.available` | []object | 已加载 Skill：`{name, description}` |
| `mcp.enabled` | bool | 是否启动 MCP 服务器 + 暴露 `mcp__` 工具 |
| `mcp.servers` | []object | 每服务器：`{name, status, command, args?, tools}` |
| `mcp.servers[].status` | string | `up` / `down` / `error` |
| `mcp.servers[].tools` | []object | 已发现工具：`{name, description}` |

**错误**：`501 tools_not_configured`（服务端未配置 Tools Provider）。

```bash
curl localhost:8080/api/v1/settings/tools
```

## PUT /api/v1/settings/tools

持久化工具配置并触发热更新（`Manager.Reconcile`）：`mcp__` 工具族就地刷新到共享 Registry；**运行中的 Run 不受影响，仅新 Run 生效**。

| 请求字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `skillsEnabled` | bool | 是 | 是否启用 Skill 工具 |
| `mcpEnabled` | bool | 是 | 是否启用 MCP |
| `mcpServers` | []object | 否 | stdio MCP 服务器列表：`{name, command, args?}` |

**响应** `200`：`{}`

**错误**：`501 tools_not_configured` / `400 bad_request` / `500 internal`。

```bash
curl -X PUT localhost:8080/api/v1/settings/tools \
  -d '{"skillsEnabled":true,"mcpEnabled":true,"mcpServers":[{"name":"fs","command":"npx","args":["-y","@modelcontextprotocol/server-filesystem","/tmp"]}]}'
```

## GET /api/v1/settings/model

读取当前模型设置（环境变量 + 服务端存储叠加后的生效值）。

**响应** `200`：

| 字段 | 类型 | 说明 |
|---|---|---|
| `provider` | string | 模型提供方标识（`openai_compat`） |
| `base_url` | string | OpenAI 兼容网关地址 |
| `model` | string | 模型名 |
| `api_key_set` | bool | API Key 是否已设置（**不回显 Key 本身**） |

```bash
curl localhost:8080/api/v1/settings/model
```

## PUT /api/v1/settings/model

更新模型设置。**空串字段 = 不修改**；`api_key` 只写不回显。

| 请求字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `provider` | string | 否 | 仅接受 `openai_compat`，其他值 400 |
| `base_url` | string | 否 | 网关地址 |
| `model` | string | 否 | 模型名 |
| `api_key` | string | 否 | API Key（写入后响应仅体现 `api_key_set: true`） |

**响应** `200`：同 GET（更新后的生效值）。

**错误**：`400 bad_request` `"unsupported provider"` / `500 internal`。

```bash
curl -X PUT localhost:8080/api/v1/settings/model \
  -d '{"model":"gpt-4o-mini","base_url":"https://api.openai.com/v1","api_key":"sk-xxx"}'
```

## 注意事项

- settings/tools 的 PUT 是全局配置（非按 owner 隔离的读写），但读取仍需认证
- MCP 子进程为独立进程组，服务优雅关闭时整组回收
- 模型设置修改后，**下一个新 Run** 使用新值；正在执行的 Run 不切换
