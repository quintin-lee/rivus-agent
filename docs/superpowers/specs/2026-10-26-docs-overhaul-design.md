# 文档与使用体验 overhaul（docs ×3 批次）设计

日期：2026-10-26
状态：已与用户逐节确认（范围结构 / 内容要点 / 交付组织，三节均 ok）

## 1. 背景与目标

上一轮「完善优化」（A-E 后端五项）已完成并提交。本轮针对**文档与使用体验**：

用户选择「全套」——按 1 新用户上手 → 2 API 参考 → 3 架构文档 顺序分批推进，
**全量中英双语**（所有文档出中英两版），API 参考采用**方案 A：手写 Markdown**（否决 OpenAPI 与混合方案）。

现状问题（已核实）：

| # | 问题 | 证据 |
|---|---|---|
| 1 | 坏链 ×3：引用不存在的 `go-eino-adk-lightweight-agent-plan.md` | `README.md:38`、`README.md:162`、`backend/README.md:4` |
| 2 | 过时：`frontend/ # 前端（预留）`，实际为完整 React 应用（20 测试文件 / 127 测试） | `README.md:37` |
| 3 | API 速览仅 9 条，实际 19 条路由 | 对照 `backend/internal/api/http/server.go:127-145` |
| 4 | `docker-compose.yml` 存在且较完整，但 README 完全未提；无 `.env.example` | 根目录 |
| 5 | `frontend/` 无 README | — |
| 6 | 无逐端点 API 参考、无 SSE 事件目录、无架构/状态机文档 | `docs/` 下仅有 `superpowers/` |

## 2. 范围与文档结构（第 1 节，已确认）

| 批次 | 内容 | 产出 |
|---|---|---|
| 1 · 新用户上手 | 修坏链、更新过时内容、补 docker 文档 | `README.md`/`README.en.md` 重写、`backend/README.md`/`README.en.md` 修正、新增 `frontend/README.md`/`README.en.md`、根 `.env.example` |
| 2 · API 参考 | 手写逐端点文档 + SSE 事件目录 | `docs/api/`（中）+ `docs/en/api/`（英） |
| 3 · 架构文档 | 组件、状态机、审批流、预算终止、事件流 | `docs/architecture/`（中）+ `docs/en/architecture/`（英） |

目标文件树：

```text
README.md / README.en.md          # 批1
backend/README.md / README.en.md  # 批1
frontend/README.md / README.en.md # 批1（新增）
.env.example                      # 批1（新增）
docs/
  api/                            # 批2（中文），英文镜像 docs/en/api/
    overview.md                   #   认证、Idempotency-Key、错误格式、预算只紧不松
    sessions.md                   #   4 路由
    runs.md                       #   5 路由（list/create/get/cancel/resume）
    events.md                     #   GET /runs/{id}/events 端点 + SSE 连接、Last-Event-ID 重放、全事件类型目录
    approvals.md                  #   2 路由
    settings.md                   #   4 路由（tools + model）
    system.md                     #   3 路由（healthz/readyz/metrics）
  architecture/                   # 批3（中文），英文镜像 docs/en/architecture/
    overview.md                   #   组件图、一次 Run 的请求→Worker→事件→SSE 生命周期（mermaid）
    state-machine.md              #   Run 状态迁移、审批流、预算超限终止、孤儿 Run 恢复
  en/                             # 英文镜像树（根/包级 README.en.md 按惯例放同级）
```

19 条路由（批 1 的 API 速览与批 2 的覆盖面以此为准，来源 `server.go:127-145`）：

```
POST   /api/v1/sessions
GET    /api/v1/sessions
GET    /api/v1/sessions/{id}
DELETE /api/v1/sessions/{id}
GET    /api/v1/runs
POST   /api/v1/runs
GET    /api/v1/runs/{id}
GET    /api/v1/runs/{id}/events
POST   /api/v1/runs/{id}/cancel
POST   /api/v1/runs/{id}/resume
POST   /api/v1/runs/{id}/approvals/{approval_id}
GET    /api/v1/runs/{id}/approvals
GET    /api/v1/settings/tools
PUT    /api/v1/settings/tools
GET    /api/v1/settings/model
PUT    /api/v1/settings/model
GET    /healthz
GET    /readyz
GET    /metrics
```

**明确不做（YAGNI）**：OpenAPI、文档生成/校验工具链、CONTRIBUTING、独立 examples/ 目录
（curl 示例内嵌于 README 与 API 文档）、中英同步自动机制。

**唯一非文档产物**：`.env.example`。不改任何 Go/TS 代码。

## 3. 各批次内容要点（第 2 节，已确认）

### 批 1 · 新用户上手

- **坏链处理 ×3**：`README.md:38`（结构树行）、`README.md:162`（详细文档段）、`backend/README.md:4`。
  批 1 先移除/改写这些引用；批 3 完成后在 README「详细文档」处补 `docs/architecture/` 入口（唯一跨批回改）。
- **过时内容**：`README.md:37` 前端描述改为实际状态（React + Vite + TS，`npx tsc --noEmit`、
  `npx vitest run` 127 测试）；项目结构树补 `docker-compose.yml`、`docs/`、`frontend/` 实况。
- **API 速览**：9 行 → 19 行（上表全量）。批 2 完成后该表保留为速览并链接 `docs/api/`。
- **Docker 快速开始**：新增章节：`cp .env.example .env` → `docker compose up -d` →
  8080（API）/ 8081（前端）、健康检查说明（backend healthcheck、`depends_on: service_healthy`）、
  数据卷 `rivus-data` 备份/持久化提示。
- **`.env.example`**：覆盖 `docker-compose.yml` 引用的全部变量（`MODEL_BASE_URL`、`MODEL_API_KEY`、
  `MODEL_NAME`、`WORKER_CONCURRENCY`、`LOG_LEVEL`、`AGENT_AUTH_TOKEN`、`DOCKER_REGISTRY`），
  并**在写作时核对 `backend/internal/config` 实际读取的环境变量**补全注释（不凭记忆写）。
- **`frontend/README.*`**：新增——dev 启动、测试（tsc/vitest）、与后端联调（`RIVUS_BACKEND`、proxy/环境变量实际机制以代码为准）。

### 批 2 · API 参考（7 篇 × 2 语）

- 每端点统一模板：方法/路径/认证 → 请求字段表 → 响应字段表 → 状态码 → curl 示例 →
  注意事项（幂等键、预算只紧不松、审批触发条件）。
- `events.md`：端点本体 `GET /api/v1/runs/{id}/events` 按统一模板写；核心是
  **从 `backend/internal/domain/event.go` 逐常量提取**（现有 28 个 `EventType`）
  事件类型目录（含 payload 字段与来源），+ SSE 连接语义、`Last-Event-ID` 断连重放、`sensitivity` 字段说明。
- `overview.md`：Bearer 认证（`AGENT_AUTH_TOKEN` 空 = 不鉴权，仅限本地开发）、
  错误响应格式（**从 handler 实际代码提取**）、`Idempotency-Key` 语义、预算收紧规则、通用 content-type。
- 字段表来源：`domain` 结构体（Run/Session/Approval 的 json tag）、handler 与 service 代码，禁止臆造。

### 批 3 · 架构文档（2 篇 × 2 语）

- `overview.md`：mermaid 组件图（app → api/http → service → runtime → store/runner/eventshub）
  + 一次 Run 的请求 → Worker → 执行 → 事件落库 → SSE 推送生命周期。
- `state-machine.md`：Run 状态迁移（**对照 `domain/task.go` 的 `ValidTransition`**）、
  审批流（`waiting_approval` → 批准/拒绝 → 恢复）、预算超限终止路径（A-E 落地后的
  `OverBudget` → `budget.exceeded` → RunFailed）、孤儿 Run 启动恢复、超时路径。

## 4. 双语策略

- 中文先定稿 → 翻译英文；文件平行镜像（`docs/api/x.md` ↔ `docs/en/api/x.md`；
  根/包级 README 用 `README.en.md` 同级惯例）。
- 两语后续漂移风险**接受**（轻量项目不建同步检查机制）。

## 5. 交付与组织（第 3 节，已确认）

- **一份 spec**（本文件）沉淀全部设计；**三个 plan 按批产出**（writing-plans → 执行 → 验收 → 下一批）。
  批 2 体量最大（7 篇 × 2 语），不与批 1/3 合并成单 plan。
- 审查：spec/plan 均**自审**（用户约束：不使用 subagent），完成后交用户复审。
- 批次依赖：批1 API 速览（19 行）→ 批2 细化（速览保留）；批3 纯新增 → 回改 README 一处
  （架构文档入口，替换批 1 移除的坏链句）。顺序固定 1→2→3。
- 提交：每批 1-2 个 commit（`docs(readme):` / `docs(api):` / `docs(architecture):`），
  直接提交 `master`（沿用本轮既有模式）。

## 6. 验收标准（每批 DoD，全部满足才算完成）

1. **链接检查**：提取全部 Markdown 内链/图片链，逐一确认目标文件存在，零坏链
2. **路由覆盖**：批 1 速览 + 批 2 端点文档 vs `server.go` mux 注册表，19/19 勾对
3. **事件覆盖**：批 2 事件目录 vs `domain/event.go` 的 `EventType` 常量，100% 对齐
4. **curl 实跑**：本地起服务（sessions/system 类端点无需模型 Key），文档内嵌示例真实执行通过
5. **回归**：`cd backend && go test ./...` 与前端 `npx vitest run` 全绿——证明零代码行为变更
6. **双语**：该批全部产出均有对应英文版，且英文版链接指向英文树内目标

## 7. 风险

| 风险 | 缓解 |
|---|---|
| 文档与代码漂移（手写方案固有） | 验收 1/2/3 每批执行；模板统一便于日后 diff |
| 中英版漂移 | 接受（§4）；本批内两语同时产出 |
| curl 示例依赖运行环境 | 只承诺无需模型 Key 的示例实跑（验收 4）；带模型的示例标注为"需配置模型三件套"并做语法级核对 |
| README 批 1 改写破坏既有读者习惯 | 保留原结构骨架（特性/快速开始/配置/命令），只修错补缺 |
