# Rivus Agent Backend

Go + CloudWeGo Eino ADK 的轻量级 AI Agent 服务：单进程、SQLite、有界 Worker。
设计文档见 [`../docs/superpowers/`](../docs/superpowers/)；仓库总览见 [`../README.md`](../README.md)。

## 目录

```text
cmd/agentd            # 服务入口
internal/
  app/                # 启动组装、优雅关闭
  config/             # 环境变量 + YAML 配置加载与校验
  api/http/           # REST、SSE、认证中间件
  domain/             # Task/Run/Step/Approval/Result、状态机、错误分类
  service/            # RunService（创建/取消/审批/恢复）、SessionService
  runtime/            # 执行编排、Eino Runner 封装、Planner、Verifier、Budget、Recovery
  model/              # 自研 OpenAI 兼容 ChatModel、ModelFactory、重试分类
  tool/               # Registry、统一执行网关、权限策略、审批绑定
  tool/builtin/       # 内置只读工具 + 受控写入工具
  skill/              # Skill 清单按需加载与校验
  store/              # SQLite 初始化/迁移、Task/Event/Approval/Checkpoint 仓储
  observability/      # slog 日志、expvar 指标
  security/           # 脱敏、密钥读取、租户归属校验
configs/              # 配置示例
skills/               # 本地 Skill 目录
```

## 快速开始

```bash
make tidy && make build
cp configs/config.example.yaml configs/config.yaml  # 可选
MODEL_BASE_URL=https://api.openai.com/v1 MODEL_API_KEY=sk-xxx MODEL_NAME=gpt-4o-mini \
  ./bin/agentd -config configs/config.yaml
```

健康检查：`curl localhost:8080/healthz`，就绪检查：`/readyz`，指标：`/metrics`。

创建会话并提交任务：

```bash
curl -X POST localhost:8080/api/v1/sessions -d '{"title":"demo"}'
curl -X POST localhost:8080/api/v1/runs \
  -H 'Idempotency-Key: demo-1' \
  -d '{"session_id":"ses_xxx","goal":"分析项目并给出重构建议","mode":"plan_execute",
       "success_criteria":["输出风险列表","每条建议有证据"],
       "budget":{"max_duration_seconds":600,"max_model_calls":30,"max_tool_calls":50,"max_iterations":12}}'
```

事件流：`GET /api/v1/runs/{id}/events`（SSE，支持断连后重放）。`tool.completed` 事件载荷含 `args` / `output` 摘要（各 1KB 截断），供前端工具调用明细展示。

运行列表搜索：`GET /api/v1/runs?q=` 同时匹配目标（goal）与结果（result_json），底层为 SQLite FTS5 trigram 全文索引（≥3 字节走短语匹配，引号自动转义；更短词条回退 LIKE，同 rowset）。

## 配置

密钥只走环境变量，不进仓库与 Prompt：

| 环境变量 | 说明 |
|---|---|
| `AGENT_CONFIG` | YAML 路径（可选，也可用 `-config` 指定） |
| `AGENT_ADDR` | 监听地址，默认 `:8080` |
| `AGENT_DB_PATH` | SQLite 文件路径 |
| `AGENT_AUTH_TOKEN` | API Bearer Token，为空则不鉴权（仅限本地开发） |
| `MODEL_BASE_URL` / `MODEL_NAME` / `MODEL_API_KEY` | OpenAI 兼容网关三件套 |
| `WORKER_CONCURRENCY` | 并发 Worker 数，默认 1，压测后再调高 |

客户端申请的预算只会被服务端上限收紧，不会放宽。

## 常用命令

```bash
make build             # 编译到 bin/agentd（含 -trimpath，生产可加 -tags prod 走 -ldflags="-s -w"）
make test              # 全量测试（单元 + 集成 + 回归）
make test-integration  # 仅 API/审批链路集成测试
make test-eval         # 仅回归评估场景
make bench             # 基准 + 并发冒烟
make vet               # go vet
make run               # 本地启动（需先 export 模型三件套）
make clean             # 清理构建产物
```

## 工具配置（Skills + MCP）

内置工具（`tool/builtin/`，注册于启动时）：`knowledge_search`（知识目录检索，只读）、`file_read`（读工作区文件，只读）、`file_list`（列目录，只读）、`file_search`（工作区全文检索，只读）、`file_edit`（精确唯一替换，默认审批）、`workspace_write`（写草稿，默认审批）、`http_fetch`（白名单出网 GET，始终审批）。目录与白名单经 `AGENT_WORKSPACE_DIR` / `AGENT_KNOWLEDGE_DIR` / `AGENT_HTTP_FETCH_ALLOWLIST` 配置。
子 agent 委派工具（`internal/subagent`，注册于启动时）：`task`（同步委派单个只读子 agent）、`tasks`（批量并行委派，默认并发 4 / 请求上限 8 / 批大小 1..5，结果按输入顺序返回，子预算=父 40% 再收紧）。
会话记忆工具（`tool/builtin/memory.go`，注册于启动时）：`memory_write`（写入会话记忆，单条 ≤2KB，每会话 LRU 保留最近 100 条，随会话删除级联清除）、`memory_read`（读取记忆，可子串过滤，大小写不敏感）。最近 10 条（≤8KB）自动注入顶层 run 指令；查询端点 `GET /api/v1/sessions/{id}/memories`。

`server_settings` 全局三键（key/value，无迁移）：

| 键 | 取值 | 说明 |
|---|---|---|
| `skills.enabled` | `"true"/"false"` | 是否注册 `invoke_skill` 工具 |
| `mcp.enabled` | `"true"/"false"` | 是否启动 MCP 服务器 + 暴露 `mcp__` 工具 |
| `mcp.servers` | JSON 数组 | MCP 服务器列表（stdio 或 http，见下） |

`mcp.servers` 示例（stdio；子进程独立进程组，`StopAll` 时整组回收）：

```json
[{"name":"fs","command":"npx","args":["-y","@modelcontextprotocol/server-filesystem","/tmp"]}]
```

远程 HTTP 服务器（`type:"http"`，POST JSON-RPC，响应兼容 JSON 与 SSE 两种形态；
`headers` 透传认证头，GET 状态接口**不回显** headers）：

```json
[{"name":"remote","type":"http","url":"https://mcp.example.com/v1","headers":{"Authorization":"Bearer <token>"}}]
```

**config.yaml `mcp:` 段与热载**：yaml 存在 `mcp:` 段时以 yaml 为权威——启动时写穿
settings 并应用；无段时 settings（API 配置）权威。配置了 yaml 文件（`-config`/`AGENT_CONFIG`）
且含 `mcp:` 段时，后台每 5s 轮询文件 mtime+size，变更即热生效（非法配置记 Warn 并保留
现状；仅 mcp 段热载，其余字段重启生效）：

```yaml
mcp:
  enabled: true
  servers:
    - name: fs
      command: npx
      args: ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
    - name: remote
      type: http
      url: https://mcp.example.com/v1
      headers:
        Authorization: Bearer <token>
```

API：`GET /api/v1/settings/tools`（读配置 + 各服务器实时状态与已发现工具）；
`PUT /api/v1/settings/tools`（持久化三键 + 触发 `Manager.Reconcile` + 就地刷新共享
`Registry` 的 `mcp__` 家族，运行中的 Agent 快照不受影响，仅新 Run 生效）。

**安全不变量（§4.3）**：`mcp__` 与 `invoke_skill` 的 `tool.Definition` 一律
`RequiredScopes` 留空（网关按 nil 作用域传参，`CheckPolicy` 对任何非空 scope 直接硬拒绝）。
MCP 工具默认 **高风险 external + 始终需审批 always**（同 `workspace_write`，走
`waiting_approval` 卡片）；`invoke_skill` 为 **只读 read + 免审批 never**，把
SKILL.md 正文作为上下文交给模型。

**运行时自愈**：故障服务器由后台 reconciler 每 30s 自动重连（`Manager.StartReconciler`，
up 的跳过、error 的重试、已移除的不再复活）；每次工具调用有 30s 超时保护，超时返回
`mcp call timeout` 错误。

v1 非目标：非致命工具调用、SSE 服务器主动通知通道、OAuth、按会话/按技能开关、密钥展开、按工具超时。

## 评估体系（Evals）

`tests/evals/` 提供基于 mock 模型的确定性评估：40 个场景（工具级 26 + agent 级 14），按
**正确性 / 效率 / 稳定性** 三维度判定，跑完输出 `eval-report.json`（含通过率与分类汇总，
已 gitignore；CI `evals` job 会将其作为产物上传）。

```bash
cd backend && make test-eval   # 等价 go test ./tests/evals/ -count=1
```

新增场景：在 `scenarios_tool_test.go` / `scenarios_agent_test.go` / `scenarios_more_test.go` / `scenarios_round14_test.go、scenarios_round15_test.go、scenarios_round16_test.go、scenarios_round19_test.go、scenarios_round21_test.go、scenarios_round25_test.go、scenarios_round26_test.go、scenarios_round27_test.go` 里追加
`Run(t, Scenario{...})` 即自动纳入报告（框架见 `framework.go`：`Expect` 声明三维度期望，
`Script` 编排 mock 模型输出，`MidRun`/`PostCheck` 处理审批与额外断言）。

## 测试布局

```text
tests/
  testutil/     # 可编排假模型 + Harness（内存 SQLite + httptest，零外部依赖）
  integration/  # API 全链路：会话/幂等/SSE/取消/审批批准与拒绝
  evals/        # 回归场景：有证据成功、无证据失败、Plan 步骤、预算必终止
  bench/        # 基准（事件写入/工具执行/建 Run）+ 20 并发冒烟
  fixtures/     # 样例请求 payload
```

## 发布（Release）

- 本地注入：`make build VERSION=v1.2.3`（Commit/BuildDate 自动取当前 git 与 UTC 时间）
- 远端发布：push tag `v*` 触发 `release` workflow——跑全量测试 → 构建 linux amd64/arm64 二进制 → `gh release create --generate-notes`
- 运行时核对：`GET /version`（免认证）返回版本三元组

## 观测（Observability）

- 每个 HTTP 响应带 `X-Request-ID`：调用方可传入（净化 + 截断 64 字符后沿用），否则自动生成 32 位 hex；`RequestIDFrom(ctx)` 可在处理链中读取。
- 请求结束输出一条结构化访问日志：`http request` + method/path/status/duration_ms/request_id（401/429/404 错误路径同样记录）；日志器经 `httpapi.WithLogger` 注入，缺省 `slog.Default()`。
- 指标：`GET /metrics`（expvar，免认证）——运行计数、工具延迟直方图分桶、`rate_limited_total`、`event_append_failed`。
- Prometheus 抓取：`GET /metrics/prometheus` 输出 Prometheus 文本格式（counter + `{key="..."}` 标签，跳过 memstats/cmdline）；与 `/metrics` 一样免认证（或配置 token 后需 Bearer）。
- 性能剖析：`GET /debug/pprof/`（heap/goroutine/allocs 等）与 `/debug/pprof/{cmdline,profile,symbol,trace}` —— **仅当配置了 `AGENT_AUTH_TOKEN`（认证开启）时注册**，且需 Bearer；本地无 token 开发模式不暴露（404）。示例：`curl -H "Authorization: Bearer $TOKEN" localhost:8080/debug/pprof/heap?debug=1`。

## 质量与维护

- Lint 门禁：`golangci-lint run ./...`（配置见 `.golangci.yml`，CI lint job 同步执行）；前端 `npm run lint`（eslint v10 flat config）。
- DB 维护：启动时输出 `db stats` 日志（页数/页大小/WAL 字节）；优雅退出先 `wal_checkpoint(TRUNCATE)` + `PRAGMA optimize` 再关闭连接（`store.Stats` / `store.Checkpoint`）。

## 备份与导出

- 在线备份：`store.Backup(db, destPath)` 基于 `VACUUM INTO` 生成一致性快照；目标文件已存在时报错（不覆盖）；`:memory:` 数据库不支持。CLI 等价：`agentd backup -o snapshot.db`（`AGENT_DB_PATH` 等环境变量与 serve 模式一致）。
- 会话导出：`agentd export -session <id> [-o out.json]` 将单个会话的 runs / events / memories 导出为 JSON（缺省写 stdout，`AGENT_DB_PATH` 等环境变量与 serve 模式一致）；单会话 run 数上限 100，超出请用备份方式。

## 说明

- Eino 锁定 `v0.9.21` 稳定版，不追 alpha；升级依赖必须跑通工具调用、流式输出、中断恢复回归。
- SQLite 驱动为 `modernc.org/sqlite`（CGO-free）；单实例部署，DB 文件放持久卷，定期备份。
- 高风险工具调用默认进人工审批（`waiting_approval`，不占 Worker），批准后恢复执行。
