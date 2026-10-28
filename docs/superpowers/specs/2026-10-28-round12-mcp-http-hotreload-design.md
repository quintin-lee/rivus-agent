# Round 12 设计：MCP 深化 — HTTP 传输 + config.yaml 热载

日期：2026-10-28 · 状态：已批准（用户 ok，选型 A+A：轮询 stat 监听 + Streamable-HTTP/SSE 响应解析）

## 1. 背景与现状

MCP 子系统（R8 批3 后）已具备：settings 驱动的服务器配置（`PUT /settings/tools` 持久化 → `manager.Reconcile` 热生效）、30s 自动重连 reconciler、30s 单次调用超时。启动时从 settings 读 `mcp.servers` 并 `SpawnAll`。

**缺口**：
1. **仅 stdio 传输**。`Transport` 唯一实现是子进程管道；`ServerSpec{Name,Command,Args}` 无 type/url 字段，无法接入远程 HTTP MCP 服务器（托管工具服务、SaaS MCP 端点）。
2. **config.yaml 配不了 MCP**。`Config` 无 mcp 段，MCP 只能通过 API 配置；K8s ConfigMap 挂载场景下运维改 yaml 无法生效。

## 2. 设计

### 批1：HTTP 传输（Streamable-HTTP + SSE 响应解析）

**协议形态**：一次 POST JSON-RPC，响应可能是 `application/json` 或 `text/event-stream`（MCP streamable HTTP 规范允许两种）。同步等待响应，天然复用 Manager 现有 30s Call 超时。不做服务器主动通知（notifications）通道。

**改动**：

| 文件 | 改动 |
|---|---|
| `internal/mcp/config.go` | `ServerSpec` += `Type string`（`stdio`\|`http`，空=stdio 向后兼容）、`URL string`、`Headers map[string]string`；`ServersFromJSON` 校验：type==http → URL 必须 http/https 且 command 可空；type 空/stdio → command 必填（现状） |
| `internal/mcp/http.go`（新） | `httpTransport`：`WriteLine(p)` = POST p 到 spec.URL，`Content-Type: application/json`，`Accept: application/json, text/event-stream`，附加 spec.Headers；存响应（非 2xx → 错误）。`ReadLine()` = 返回响应体——JSON 直接给字节；`text/event-stream` 解析 SSE 帧取匹配请求 id 的 `data:` 载荷。一次 WriteLine 一次 ReadLine，与 `client.rpc` 同步模型一致。`Stop()` = 关闭空闲连接。HTTP client 超时 35s（略大于 Manager 30s，让上层超时先到） |
| `internal/mcp/transport.go` | `NewSpecTransportFactory`：按 `spec.Type` 分发 http → `httpTransport`，否则 stdio（现 `StdioTransportFactory` 保留，`app.go` 改用 spec factory） |
| `internal/api/http/server.go` | `MCPServerIn` / `MCPServerInfo` += `type`/`url`/`headers` 字段透传 |
| `internal/app/toolconfig.go` | `mcpServersFromIn` 映射新字段 |

**测试**（`http_test.go` + 既有文件追加）：httptest JSON 回复跑通 initialize→tools/list→tools/call；SSE 响应解析；header 注入断言；401 → 错误；URL 校验；factory 分发。

### 批2：config.yaml `mcp:` 段 + 启动合并

**改动**：

| 文件 | 改动 |
|---|---|
| `internal/config/config.go` | `Config` += `MCP *MCPConfig yaml:"mcp,omitempty"`；`MCPConfig{Enabled bool, Servers []MCPServerYaml}`；`MCPServerYaml{name,command,args,type,url,headers}` 镜像结构（config 包不 import mcp，避免反向依赖）；`Default()` 不设置 MCP（nil） |
| `internal/app/app.go` | 启动合并（写穿语义）：`cfg.MCP != nil` → 把 yaml 值覆盖写入 settings 两个 key（`mcp.enabled`/`mcp.servers`），再走现有 `ReadToolConfig` → `SpawnAll`；`cfg.MCP == nil` → 现状（settings 权威） |

**语义**：yaml 有 mcp 段 = yaml 权威（每次启动重放，API 修改在下次重启被 yaml 覆盖——运维显式接管）；yaml 无段 = API/settings 权威（现状）。

**测试**：config 解析（含 mcp 段/缺省）；合并语义单测（yaml 有段覆盖 settings、无段保留 settings）。

### 批3：文件监听热载

**改动**：

| 文件 | 改动 |
|---|---|
| `internal/app/app.go`（或新 `hotreload.go`） | 提取 `applyMCPConfig(ctx, cfg, settings, manager, registry)`：写穿 settings + `manager.Reconcile` + `registry.Replace("mcp__", ...)`（复用 SaveTools 的 mcp 部分，不含 skill 部分）。`cfgPath != ""` 时启动 watcher goroutine：5s ticker（interval 可注入）`os.Stat` 比较 mtime+size，变化 → `config.Load(cfgPath)` → `cfg.MCP != nil` → `applyMCPConfig`，成功 `log.Info("mcp config reloaded")`，失败 `log.Warn` 保留旧配置。生命周期挂 app ctx/Stop |
| `cmd/agentd/main.go` | 无改动（signal ctx 已传入） |

**范围**：仅 mcp 段热应用；其他 yaml 字段（addr/db/限流等）不热载（重启生效，spec 明示）。

**测试**：`applyMCPConfig` 单测（覆盖/禁用/非法 spec 报错不落库）；watcher 集成单测（临时 yaml，改文件，注入短 interval，断言 N 秒内 settings/registry 生效；yaml 非法时旧配置保留）。

## 3. 非目标（YAGNI）

- SSE 服务器主动通知 / progress 通知通道
- OAuth / 复杂认证（仅静态 Headers）
- fsnotify 依赖（轮询 stat 对 ConfigMap symlink 替换更稳）
- yaml 其他字段热载
- MCP sampling / roots 等 capabilities

## 4. 验收标准

1. HTTP MCP 服务器（httptest 模拟，JSON 与 SSE 两种响应）经 `initialize→tools/list→tools/call` 全流程可用，工具注册进 `mcp__` 家族
2. `PUT /settings/tools` 可配置 type/url/headers，GET status 回显
3. config.yaml `mcp:` 段：启动写穿 settings；无段时 settings 权威
4. 修改 yaml mcp 段文件，≤10s 内热生效（settings/registry 更新），非法 yaml 不破坏现状
5. stdio 既有行为零回归（全部既有测试绿）
6. 全量验收：`go build/vet/test` + `make test-eval`（20 场景）+ golangci 0 issues + 前端 `tsc`/`vitest`（零漂移）
7. 文档：backend README zh/en MCP 段更新（HTTP 传输 + yaml 热载）；spec 偏差回写

## 5. 风险

- **SSE 解析兼容性**：仅解析单事件 `data:` 载荷（`data:` 可能多行拼接）；多行 data 按规范拼接后整体 parse
- **长连接 MCP**（GET /sse 旧版协议）不支持——新规范服务器均支持 streamable HTTP；遇旧版报错信息明确
- **热载竞态**：watcher 与 API SaveTools 并发写 settings/Reconcile —— 两者都经 `manager` 内部锁 + registry 锁，最终一致；watcher 应用前重新 Load 全量配置，无增量漂移
- **yaml 覆盖 API 配置**：文档明示语义（yaml 有段=接管），避免用户误以为 API 修改丢失是 bug
