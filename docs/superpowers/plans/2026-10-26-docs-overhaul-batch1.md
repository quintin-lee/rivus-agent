# Docs Overhaul 批1 · 新用户上手 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:executing-plans to implement this plan (本仓库约束：不使用 subagent)。Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复全部坏链与过时内容，把 README 的 API 速览补全到 19 条路由，补齐 Docker 快速开始与 `.env.example`，新增 frontend README —— 中英双语。

**Architecture:** 纯文档批次：只改 Markdown + 新增 `.env.example`，零 Go/TS 代码变更。所有事实（环境变量、路由、端口、脚本）已在计划中核实固化，执行时照抄即可，不需再推断。

**Tech Stack:** Markdown（手写，中英双语）、bash 校验脚本、`docker compose config` 可选检查。

**Spec:** `docs/superpowers/specs/2026-10-26-docs-overhaul-design.md`（§2 批1、§3 批1、§6 验收 1/2/5/6）

**已核实事实（写入文档时直接引用，勿重新猜测）：**

- 配置环境变量（`backend/internal/config/config.go:76-105`）：`AGENT_ADDR`、`AGENT_DB_PATH`、`AGENT_SKILLS_DIR`、`AGENT_AUTH_TOKEN`、`MODEL_PROVIDER`、`MODEL_NAME`、`MODEL_BASE_URL`、`MODEL_API_KEY`、`WORKER_CONCURRENCY`、`LOG_LEVEL`；另有 `-config` / `AGENT_CONFIG` 指定 YAML
- `docker-compose.yml` 引用变量：`DOCKER_REGISTRY`、`MODEL_BASE_URL`（必填 `:?`）、`MODEL_API_KEY`（必填 `:?`）、`MODEL_NAME`、`WORKER_CONCURRENCY`、`LOG_LEVEL`、`AGENT_AUTH_TOKEN`
- compose 端口：backend `8080:8080`（healthcheck `wget /healthz`），frontend `8081:8080`（`depends_on: service_healthy`，`RIVUS_BACKEND=http://backend:8080`）；数据卷 `rivus-data:/data`；挂载 `./backend/skills:/app/skills`
- 前端 dev：`npm run dev` = vite，端口 5173，proxy `/api`、`/healthz`、`/readyz` → `VITE_BACKEND_URL`（缺省 `http://localhost:8080`）（`frontend/vite.config.ts`）
- 前端脚本：`dev` / `build`（`tsc && vite build`）/ `preview` / `test`（vitest）/ `lint`（eslint）；一次性跑测试用 `npx vitest run`；类型检查 `npx tsc --noEmit`
- 前端 Docker：pnpm 构建 + nginx；`start.sh` 用 `envsubst` 渲染 `nginx.conf.template`（`RIVUS_BACKEND` 缺省 `http://localhost:8080`），nginx 代理 `/api`、`/healthz`、`/readyz`、`/metrics`
- Makefile 目标（backend/）：`help build test test-integration test-eval bench vet tidy run clean`
- 19 条路由（`backend/internal/api/http/server.go:127-145`）：见下方 Task 3 表格
- 坏链位置：`README.md:38`（结构树）、`README.md:162`（详细文档段）、`backend/README.md:4`
- 过时位置：`README.md:37` `frontend/ # 前端（预留）`

---

## Chunk 1: 中文文档

### Task 1: `.env.example`

**Files:**
- Create: `.env.example`

- [ ] **Step 1.1: 写入文件**

```bash
# Rivus Agent - docker compose 环境变量示例
# 用法：cp .env.example .env && 编辑 .env 后 docker compose up -d

# ---- 模型（必填：OpenAI 兼容网关）----
MODEL_BASE_URL=https://api.openai.com/v1
MODEL_API_KEY=sk-xxx
MODEL_NAME=gpt-4o-mini
# 可选：模型提供方标识（环境变量 MODEL_PROVIDER，缺省走 OpenAI 兼容协议）
MODEL_PROVIDER=

# ---- 服务 ----
# Bearer Token，为空则不鉴权（仅限本地开发）
AGENT_AUTH_TOKEN=
# Worker 并发数（环境变量 WORKER_CONCURRENCY，默认 1）
WORKER_CONCURRENCY=1
# 日志级别 debug|info|warn|error（环境变量 LOG_LEVEL，默认 info）
LOG_LEVEL=info

# ---- 可选 ----
# 镜像加速前缀，如 registry.1ms.run/（仅构建期使用）
DOCKER_REGISTRY=
# 其余环境变量（AGENT_ADDR、AGENT_DB_PATH、AGENT_SKILLS_DIR）由
# docker-compose.yml 内置默认值，如需覆盖请在 compose 层配置。
```

- [ ] **Step 1.2: 校验 compose 引用全覆盖**

Run:
```bash
grep -oE '\$\{[A-Z_]+[:?]' docker-compose.yml | tr -d '${:? ' | sort -u | while read v; do grep -q "^$v=" .env.example && echo "OK $v" || echo "MISSING $v"; done
```
Expected: 每行 `OK`，无 `MISSING`（7 个：DOCKER_REGISTRY, MODEL_BASE_URL, MODEL_API_KEY, MODEL_NAME, WORKER_CONCURRENCY, LOG_LEVEL, AGENT_AUTH_TOKEN）

- [ ] **Step 1.3: Commit**

```bash
git add .env.example
git commit -m "docs: add .env.example covering docker-compose and config env vars"
```

### Task 2: README.md（中文）修正与补全

**Files:**
- Modify: `README.md`

- [ ] **Step 2.1: 修结构树（37-38 行）**

将：
```text
├── frontend/                 # 前端（预留）
└── go-eino-adk-lightweight-agent-plan.md  # 技术方案
```
替换为：
```text
├── frontend/                 # 前端（React + Vite + TS + Tailwind，127 测试）
├── docs/                     # 设计文档（superpowers/specs、plans）
├── docker-compose.yml        # 一键启动（backend + frontend）
└── .env.example              # 环境变量示例
```

- [ ] **Step 2.2: API 速览表 9 → 19 行**

定位 `## API 速览` 下的表格，整表替换为（19 行，顺序与 `server.go:127-145` 注册一致）：

```markdown
| 方法 | 路径 | 用途 |
|---|---|---|
| POST | `/api/v1/sessions` | 创建会话 |
| GET | `/api/v1/sessions` | 会话列表 |
| GET | `/api/v1/sessions/{id}` | 获取会话 |
| DELETE | `/api/v1/sessions/{id}` | 删除会话 |
| GET | `/api/v1/runs` | Run 列表 |
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
```

- [ ] **Step 2.3: 新增 Docker 快速开始章节**

插入位置：`## 快速开始` 章节结束之后、`## API 速览` 之前。内容：

```markdown
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
```

- [ ] **Step 2.4: 配置表补 3 行**

`## 配置` 表格末尾（`WORKER_CONCURRENCY` 行之后）追加：

```markdown
| `MODEL_PROVIDER` | 模型提供方标识，缺省走 OpenAI 兼容协议 |
| `AGENT_SKILLS_DIR` | 本地 Skill 目录，缺省 `./skills` |
| `LOG_LEVEL` | 日志级别 `debug/info/warn/error`，默认 `info` |
```

- [ ] **Step 2.5: 修详细文档坏链（162 行）**

将整句：
```markdown
完整技术方案（含架构设计、状态机、测试策略、风险评估）见 [`go-eino-adk-lightweight-agent-plan.md`](./go-eino-adk-lightweight-agent-plan.md)。
```
替换为：
```markdown
设计文档（specs / plans）见 [`docs/superpowers/`](./docs/superpowers/)。
```

- [ ] **Step 2.6: 自检**

Run:
```bash
grep -c "go-eino-adk-lightweight-agent-plan" README.md   # Expected: 0
grep -c "预留" README.md                                  # Expected: 0
grep -cE '^\| (GET|POST|PUT|DELETE) \|' README.md        # Expected: 19
```

### Task 3: README.en.md（中文版的英文镜像）

**Files:**
- Create: `README.en.md`

- [ ] **Step 3.1: 基于最终中文版全文翻译**

要求：
- 结构、标题层级、表格行列与中文版**逐一对应**
- 技术名词保留原样：`Idempotency-Key`、`Last-Event-ID`、`waiting_approval`、`WAL`、`expvar` 等
- 链接指向英文树：`backend/README.en.md`、`frontend/README.en.md`、`docs/superpowers/`（specs/plans 目前仅中文，直接链原路径可接受）
- curl 示例与中文版完全一致（代码块不翻译注释以外的命令）

- [ ] **Step 3.2: 校验与中文版结构一致**

Run:
```bash
grep -cE '^## ' README.md README.en.md    # 两文件二级标题数一致
grep -cE '^\| (GET|POST|PUT|DELETE) \|' README.en.md  # Expected: 19
grep -c "go-eino-adk-lightweight-agent-plan" README.en.md  # Expected: 0
```

### Task 4: backend/README 修正（中+英）

**Files:**
- Modify: `backend/README.md`
- Create: `backend/README.en.md`

- [ ] **Step 4.1: 修坏链**

将 `backend/README.md` 第 4 行：
```markdown
详见方案文档 `../go-eino-adk-lightweight-agent-plan.md`。
```
替换为：
```markdown
设计文档见 [`../docs/superpowers/`](../docs/superpowers/)；仓库总览见 [`../README.md`](../README.md)。
```

- [ ] **Step 4.2: 校验**

Run: `grep -c "go-eino-adk-lightweight-agent-plan" backend/README.md` → Expected: 0

- [ ] **Step 4.3: 创建 `backend/README.en.md`**

以修正后的中文版为底全文翻译。其内部链接改为英文树：
`../README.en.md`、`../docs/superpowers/`。校验：
```bash
grep -c "go-eino-adk-lightweight-agent-plan" backend/README.en.md  # 0
```

### Task 5: frontend/README（新增，中+英）

**Files:**
- Create: `frontend/README.md`
- Create: `frontend/README.en.md`

- [ ] **Step 5.1: 写入 `frontend/README.md`**

```markdown
# Rivus Agent 前端

React 18 + Vite + TypeScript + Tailwind CSS + Zustand 的对话式界面，
经代理访问后端 REST / SSE 接口。

## 快速开始

```bash
npm install        # Docker 镜像构建使用 pnpm，本地开发用 npm 即可
npm run dev        # http://localhost:5173
```

开发服务器将 `/api`、`/healthz`、`/readyz` 代理到后端
（`VITE_BACKEND_URL` 环境变量可改指向，缺省 `http://localhost:8080`）。
后端启动见 [根 README](../README.md) 或 `docker compose up -d`。

## 常用命令

```bash
npm run build               # tsc && vite build → dist/
npm run preview             # 预览构建产物
npm run lint                # ESLint
npx tsc --noEmit            # 仅类型检查
npx vitest run              # 单次跑全部测试（20 文件 / 127 用例）
```

## Docker

镜像为多阶段构建（pnpm 构建 + nginx 托管），`start.sh` 用 `envsubst` 将
`RIVUS_BACKEND`（缺省 `http://localhost:8080`）渲染进 nginx 配置，
反代 `/api`、`/healthz`、`/readyz`、`/metrics`。`docker compose` 场景下
前端映射 `8081→8080`，`RIVUS_BACKEND=http://backend:8080` 由 compose 注入。

## 目录

```text
src/
  components/   # 对话、Run 卡片、审批卡片等
  store/        # Zustand 状态
  lib/          # API/SSE 客户端与工具函数
  App.tsx       # 根布局
```
```

- [ ] **Step 5.2: 创建英文镜像 `frontend/README.en.md`**

同样结构翻译；内部链接指向 `../README.en.md`。

## Chunk 2: 验证与收尾

### Task 6: 验收检查（spec §6 中适用于批 1 的条目）

- [ ] **Step 6.1: 链接检查（零坏链）**

Run（逐文件提取相对链接并验证目标存在）：
```bash
for f in README.md README.en.md backend/README.md backend/README.en.md frontend/README.md frontend/README.en.md; do
  d=$(dirname "$f")
  grep -oE '\]\(([^)#]+)' "$f" | sed 's/](//' | grep -vE '^(https?|mailto):' | while read -r t; do
    t=${t%%#*}
    [ -z "$t" ] && continue
    [ -e "$d/$t" ] || echo "BROKEN: $f -> $t"
  done
done
```
Expected: 无 `BROKEN` 输出

- [ ] **Step 6.2: 路由 19/19 覆盖**

Run:
```bash
grep -cE 'HandleFunc|mux.Handle' backend/internal/api/http/server.go   # 19
grep -cE '^\| (GET|POST|PUT|DELETE) \|' README.md                      # 19
grep -cE '^\| (GET|POST|PUT|DELETE) \|' README.en.md                   # 19
```
Expected: 19 / 19 / 19

- [ ] **Step 6.3: curl 实跑（无需模型 Key）**

```bash
cd backend && make build
AGENT_ADDR=:18099 AGENT_DB_PATH=/tmp/rivus-docs-smoke.db \
  MODEL_BASE_URL=http://127.0.0.1:9 MODEL_API_KEY=dummy MODEL_NAME=dummy \
  ./bin/agentd >/tmp/rivus-docs-smoke.log 2>&1 &
echo $! > /tmp/rivus-docs-smoke.pid
sleep 2
curl -sf localhost:18099/healthz && echo " healthz OK"
curl -sf localhost:18099/readyz  >/dev/null && echo "readyz OK"
curl -sf localhost:18099/metrics | grep -q latency_ms_0_50 && echo "metrics OK"
SES=$(curl -sf -X POST localhost:18099/api/v1/sessions -d '{"title":"docs-smoke"}' | grep -oE '"id":"[^"]+"' | head -1 | cut -d'"' -f4)
[ -n "$SES" ] && curl -sf localhost:18099/api/v1/sessions/$SES >/dev/null && echo "session OK"
kill "$(cat /tmp/rivus-docs-smoke.pid)" && rm -f /tmp/rivus-docs-smoke.pid /tmp/rivus-docs-smoke.db*
```
Expected: 逐行 `OK`；若启动失败，读 `/tmp/rivus-docs-smoke.log` 排查（端口冲突换端口）
注意：带模型的示例（POST /runs 等）**不做实跑**，仅做语法核对（spec §7 风险 3）。

- [ ] **Step 6.4: 回归（证明零代码变更）**

```bash
cd backend && go test ./... -count=1 -timeout 600s
cd ../frontend && npx tsc --noEmit && npx vitest run
```
Expected: 全绿（后端全包 ok；前端 20 文件 / 127 测试）

- [ ] **Step 6.5: 双语齐全**

Run:
```bash
for f in README backend/README frontend/README; do [ -f "$f.md" ] && [ -f "$f.en.md" ] && echo "OK $f" || echo "MISSING $f"; done
```
Expected: 3 × `OK`

- [ ] **Step 6.6: Commit**

```bash
git add README.md README.en.md backend/README.md backend/README.en.md frontend/README.md frontend/README.en.md
git commit -m "docs(readme): fix dead links, full 19-route API table, docker quick start, frontend readme (zh/en)"
git status --short   # Expected: 仅 ?? .omo/ （Task 1 已提交 .env.example）
```

---

## 自审记录

| 检查项 | 结论 |
|---|---|
| 覆盖 spec 批1 全部要点（坏链×3、过时×1、19 表、docker、.env.example、frontend README） | ✅ Task 2/4/1/3/5 |
| 每步有精确锚点/完整内容/可执行命令与预期输出 | ✅ |
| 验收映射 spec §6：链接(6.1)、路由(6.2)、curl(6.3)、回归(6.4)、双语(6.5) | ✅ |
| EN 文件策略：可从最终中文版派生的（README.en/backend README.en）给翻译要求而非全文内联，避免计划内嵌注定过期的副本 | ✅（有意偏离"complete code"条款：文档翻译以执行时中文定稿为唯一来源） |
| 零代码约束：Task 6.4 回归证明 | ✅ |
| 已知风险：agentd 启动参数/config 校验可能拒绝 dummy 模型三件套 → Step 6.3 备注了读日志排查 | ⚠️ 执行时如失败，改为仅校验 healthz 所需的最小环境并记录 |
