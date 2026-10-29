# Rivus Agent

基于 **Go + CloudWeGo Eino ADK** 的轻量级 AI Agent 服务。单进程、SQLite、有界 Worker，支持可规划、可调用工具、可暂停恢复、可审计的通用 Agent 能力。

## 特性

- **双执行模式**：默认 ReAct 工具循环；复杂任务可切换 Plan-Execute
- **人工审批**：高风险工具调用进入 `waiting_approval` 状态，不占用 Worker，批准后恢复
- **中断恢复**：SQLite Checkpoint 持久化 Eino 执行状态，启动时自动扫描孤儿 Run
- **预算控制**：每 Run 的时间、模型调用数、工具调用数、迭代次数、输出大小均有上限
- **安全治理**：工具权限分级（只读 / 写入 / 外部副作用 / 禁止）、参数审批绑定、敏感字段脱敏
- **SSE 事件流**：支持断连重放，按 `Last-Event-ID` 恢复

## 项目结构

```text
rivus-agent/
├── backend/                  # Go 服务
│   ├── cmd/agentd/           # 入口
│   ├── internal/
│   │   ├── app/              # 启动组装、优雅关闭
│   │   ├── config/           # 环境变量 + YAML 配置加载
│   │   ├── api/http/         # REST + SSE + 认证
│   │   ├── domain/           # 状态机、错误分类
│   │   ├── service/          # RunService / SessionService
│   │   ├── runtime/          # Eino Runner、Planner、Verifier、Budget、Recovery
│   │   ├── model/            # OpenAI 兼容 ModelFactory、重试分类
│   │   ├── tool/             # Registry、统一执行网关、权限策略、审批
│   │   ├── skill/            # Skill 按需加载与校验
│   │   ├── store/            # SQLite 仓储（Task/Event/Approval/Checkpoint）
│   │   ├── observability/    # slog 日志、expvar 指标
│   │   └── security/         # 脱敏、密钥、租户归属
│   ├── configs/              # 配置示例
│   ├── skills/               # 本地 Skill 目录
│   ├── tests/                # 集成测试
│   └── Makefile
├── frontend/                 # 前端（React + Vite + TS + Tailwind，127 测试）
├── docs/                     # 设计文档（superpowers/specs、plans）
├── docker-compose.yml        # 一键启动（backend + frontend）
└── .env.example              # 环境变量示例
```

## 快速开始

**环境要求**：Go 1.27+，无需 CGO。

```bash
# 进入 backend
cd backend

# 编译
make build

# 配置模型（环境变量）
export MODEL_BASE_URL=https://api.openai.com/v1
export MODEL_API_KEY=sk-xxx
export MODEL_NAME=gpt-4o-mini

# 可选：创建本地配置
cp configs/config.example.yaml configs/config.yaml

# 启动
./bin/agentd -config configs/config.yaml
```

验证服务：

```bash
curl localhost:8080/healthz   # 存活
curl localhost:8080/readyz    # 就绪
curl localhost:8080/metrics   # expvar 指标
```

创建会话并提交任务：

```bash
# 创建会话
curl -X POST localhost:8080/api/v1/sessions -d '{"title":"demo"}'

# 提交 Run
curl -X POST localhost:8080/api/v1/runs \
  -H 'Idempotency-Key: demo-1' \
  -d '{
    "session_id": "ses_xxx",
    "goal": "分析项目并给出重构建议",
    "mode": "plan_execute",
    "success_criteria": ["输出风险列表", "每条建议有证据"],
    "budget": {
      "max_duration_seconds": 600,
      "max_model_calls": 30,
      "max_tool_calls": 50,
      "max_iterations": 12
    }
  }'

# 订阅事件流（SSE，支持 Last-Event-ID 重放）
curl -N localhost:8080/api/v1/runs/<run_id>/events
```

## Docker 快速开始

```bash
cp .env.example .env        # 填入 MODEL_BASE_URL / MODEL_API_KEY / MODEL_NAME
docker compose up -d
```

- 后端 API：`http://localhost:8080`（healthcheck 通过 `wget /healthz` 探测，
  前端容器 `depends_on: service_healthy`，就绪后自动启动）
- 前端界面：`http://localhost:8081`（nginx 静态托管，反代 `/api`、`/healthz`、
  `/readyz`、`/metrics` 至后端容器）
- 数据持久化：数据卷 `rivus-data` → 容器内 `/data/agent.db`（SQLite，WAL 模式）；
  后端 skills 目录以 `./backend/skills` 只读挂载进容器
- 镜像加速：`.env` 中设置 `DOCKER_REGISTRY=registry.1ms.run/` 等前缀

## API 速览

| 方法 | 路径 | 用途 |
|---|---|---|
| POST | `/api/v1/sessions` | 创建会话 |
| GET | `/api/v1/sessions` | 会话列表 |
| GET | `/api/v1/sessions/{id}` | 获取会话 |
| GET | `/api/v1/sessions/{id}/memories` | 会话记忆列表 |
| DELETE | `/api/v1/sessions/{id}` | 删除会话 |
| PATCH | `/api/v1/sessions/{id}` | 会话重命名 |
| GET | `/api/v1/runs` | Run 列表（带 `session_id` 列该会话；省略则跨会话顶层历史；支持 `status` 过滤） |
| POST | `/api/v1/runs` | 提交任务（支持 `Idempotency-Key`） |
| GET | `/api/v1/runs/{id}` | 查看 Run 状态与结果 |
| GET | `/api/v1/runs/{id}/events` | SSE 事件流（`Last-Event-ID` 重放） |
| POST | `/api/v1/runs/{id}/cancel` | 取消 Run |
| POST | `/api/v1/runs/{id}/resume` | 恢复暂停 / 中断任务 |
| POST | `/api/v1/runs/{id}/approvals/{approval_id}` | 批准 / 拒绝 |
| GET | `/api/v1/runs/{id}/approvals` | 审批列表 |
| GET | `/api/v1/settings/tools` | 读取 Skills/MCP 工具配置 |
| PUT | `/api/v1/settings/tools` | 更新 Skills/MCP 工具配置 |
| GET | `/api/v1/settings/model` | 读取模型设置 |
| PUT | `/api/v1/settings/model` | 更新模型设置 |
| GET | `/healthz` | 存活检查 |
| GET | `/readyz` | 就绪检查 |
| GET | `/metrics` | expvar 指标（含延迟直方图桶） |
| GET | `/version` | 构建版本信息（免认证） |

## 配置

| 环境变量 / 配置项 | 说明 |
|---|---|
| `AGENT_CONFIG` / `-config` | YAML 配置文件路径（可选） |
| `AGENT_ADDR` | 监听地址，默认 `:8080` |
| `AGENT_DB_PATH` | SQLite 文件路径，默认 `./data/agent.db` |
| `AGENT_AUTH_TOKEN` | API Bearer Token；为空则不鉴权（仅限本地开发） |
| `MODEL_BASE_URL` | OpenAI 兼容网关地址 |
| `MODEL_NAME` | 模型名称 |
| `MODEL_API_KEY` | API Key |
| `WORKER_CONCURRENCY` | 并发 Worker 数，默认 1 |
| `MODEL_PROVIDER` | 模型提供方标识，缺省走 OpenAI 兼容协议 |
| `AGENT_SKILLS_DIR` | 本地 Skill 目录，缺省 `./skills` |
| `AGENT_WORKSPACE_DIR` | 工具工作区根目录（file_list/file_search/file_edit/workspace_write 的读写范围），缺省 `./workspace` |
| `AGENT_KNOWLEDGE_DIR` | 知识库目录（knowledge_search 检索范围），缺省 `./knowledge` |
| `AGENT_HTTP_FETCH_ALLOWLIST` | http_fetch 域名白名单（逗号分隔，精确匹配；为空 = 完全禁用出网） |
| `AGENT_RATE_LIMIT_RPM` | API 速率限制（每分钟每客户端请求数，按 X-Owner-ID 或来源 IP 分桶；0 = 关闭，默认 0） |
| `LOG_LEVEL` | 日志级别 `debug/info/warn/error`，默认 `info` |

YAML 配置示例见 `backend/configs/config.example.yaml`。

客户端申请的预算上限只会被服务端策略收紧，不会放宽。

## 常用命令

```bash
make build   # 编译到 bin/agentd（含 -trimpath）
make test    # 全量单元测试
make vet     # go vet
make run     # 本地启动（需先 export 模型三件套）
make clean   # 清理构建产物
```

## 技术选型

| 层次 | 选择 |
|---|---|
| 语言 | Go 1.27+，CGO-free |
| Agent 框架 | CloudWeGo Eino ADK `v0.9.21` |
| 持久化 | SQLite（`modernc.org/sqlite`），WAL 模式 |
| 模型适配 | OpenAI 兼容 Chat Completions |
| 日志 | `log/slog` 结构化日志 |
| 指标 | `expvar`（内置） |

## 安全说明

- 密钥只走环境变量，不写入仓库或 Prompt
- 工具权限每次调用均重新校验，不缓存
- 高风险副作用操作默认需人工审批，审批绑定参数摘要 + 过期时间
- 日志 / 事件 / 错误信息中敏感字段自动脱敏
- 单实例部署，SQLite 文件需挂载持久卷并定期备份

## 详细文档

- 架构设计：[`docs/architecture/`](./docs/architecture/)（组件图、Run 生命周期、状态机）
- API 参考：[`docs/api/`](./docs/api/)（逐端点 + SSE 事件目录）
- 设计文档（specs / plans）：[`docs/superpowers/`](./docs/superpowers/)
