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
| `mcp.servers` | JSON 数组 | stdio MCP 服务器列表（见下） |

`mcp.servers` 示例（stdio；子进程独立进程组，`StopAll` 时整组回收）：

```json
[{"name":"fs","command":"npx","args":["-y","@modelcontextprotocol/server-filesystem","/tmp"]}]
```

API：`GET /api/v1/settings/tools`（读配置 + 各服务器实时状态与已发现工具）；
`PUT /api/v1/settings/tools`（持久化三键 + 触发 `Manager.Reconcile` + 就地刷新共享
`Registry` 的 `mcp__` 家族，运行中的 Agent 快照不受影响，仅新 Run 生效）。

**安全不变量（§4.3）**：`mcp__` 与 `invoke_skill` 的 `tool.Definition` 一律
`RequiredScopes` 留空（网关按 nil 作用域传参，`CheckPolicy` 对任何非空 scope 直接硬拒绝）。
MCP 工具默认 **高风险 external + 始终需审批 always**（同 `workspace_write`，走
`waiting_approval` 卡片）；`invoke_skill` 为 **只读 read + 免审批 never**，把
SKILL.md 正文作为上下文交给模型。

v1 非目标：自动重连、非致命工具调用、HTTP/SSE 传输、按会话/按技能开关、密钥展开、按工具超时。

## 评估体系（Evals）

`tests/evals/` 提供基于 mock 模型的确定性评估：15 个场景（工具级 8 + agent 级 7），按
**正确性 / 效率 / 稳定性** 三维度判定，跑完输出 `eval-report.json`（含通过率与分类汇总，
已 gitignore；CI `evals` job 会将其作为产物上传）。

```bash
cd backend && make test-eval   # 等价 go test ./tests/evals/ -count=1
```

新增场景：在 `scenarios_tool_test.go` / `scenarios_agent_test.go` 里追加
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

## 说明

- Eino 锁定 `v0.9.21` 稳定版，不追 alpha；升级依赖必须跑通工具调用、流式输出、中断恢复回归。
- SQLite 驱动为 `modernc.org/sqlite`（CGO-free）；单实例部署，DB 文件放持久卷，定期备份。
- 高风险工具调用默认进人工审批（`waiting_approval`，不占 Worker），批准后恢复执行。
