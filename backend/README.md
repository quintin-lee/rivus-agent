# Rivus Agent Backend

Go + CloudWeGo Eino ADK 的轻量级 AI Agent 服务：单进程、SQLite、有界 Worker。
详见方案文档 `../go-eino-adk-lightweight-agent-plan.md`。

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

事件流：`GET /api/v1/runs/{id}/events`（SSE，支持断连后重放）。

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
make build   # 编译到 bin/agentd（含 -trimpath，生产可加 -tags prod 走 -ldflags="-s -w"）
make test    # 全量单元测试
make vet     # go vet
make run     # 本地启动（需先 export 模型三件套）
make clean   # 清理构建产物
```

## 说明

- Eino 锁定 `v0.9.21` 稳定版，不追 alpha；升级依赖必须跑通工具调用、流式输出、中断恢复回归。
- SQLite 驱动为 `modernc.org/sqlite`（CGO-free）；单实例部署，DB 文件放持久卷，定期备份。
- 高风险工具调用默认进人工审批（`waiting_approval`，不占 Worker），批准后恢复执行。
