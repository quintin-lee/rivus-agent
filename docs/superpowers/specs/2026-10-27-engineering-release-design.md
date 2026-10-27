# 工程化深化设计 — 版本端点 + 构建注入 + 发布流水线（Round 7）

日期：2026-10-27 ｜ 状态：已确认（方案 A，用户选方向 1 后按静默先例确认 A）
前序：六轮「完善优化」已完成（后端 A-E 加固、双语文档、Agent 能力、评估体系、前端补全+载荷增强、安全加固）。

## 1. 背景与问题

- 运维无法判断线上跑的是哪个版本：无 version 端点，二进制无版本信息（`make build` 与 Docker 构建均不注入）
- 无发布流水线：`.github/workflows/ci.yml` 仅 3 个 job（backend/frontend/evals），打 tag 不产生 Release 产物
- server.go 716 行为最大文件（结构治理留给后续轮次，本轮不做）

## 2. 方案

**A（采纳）：独立 `buildinfo` 包 + `GET /version` 端点 + ldflags 注入 + release workflow。**
ldflags 直接注入包变量，main/server 零接线；`/version` 免认证（与 healthz/readyz 同级，仅暴露版本/commit/构建时间，无敏感信息）；/healthz 保持纯探针语义。

**B（否决）：版本并入 /healthz 响应。** 污染探针语义，且 ldflags 需 `-X main.` 再经 app→server 三层传递，接线更多。

## 3. 详细设计

### 3.1 buildinfo 包 + GET /version

- 新增 `backend/internal/buildinfo/buildinfo.go`：
  ```go
  package buildinfo
  // ldflags 注入目标（-X rivus-agent-backend/internal/buildinfo.Version=...）
  var (
      Version   = "dev"
      Commit    = "unknown"
      BuildDate = "unknown"
  )
  ```
- server.go：`s.mux.HandleFunc("GET /version", s.handleVersion)`（readyz 之后）；`handleVersion` → `writeJSON(w, 200, map[string]string{"version":..., "commit":..., "build_date":...})`
- auth 豁免（server.go L175 附近）：`strings.HasPrefix(r.URL.Path, "/version")` 加入 /healthz /readyz 同款前缀判断
- 速率限制零改动：rateLimit 中间件只对 `/api/v1/` 前缀生效，/version 自动豁免

### 3.2 构建注入（Makefile + Dockerfile）

- `backend/Makefile`：
  ```make
  VERSION ?= dev
  COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
  DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo unknown)
  LDFLAGS := -X $(MODULE)/internal/buildinfo.Version=$(VERSION) -X $(MODULE)/internal/buildinfo.Commit=$(COMMIT) -X $(MODULE)/internal/buildinfo.BuildDate=$(DATE)
  build: ; go build $(GOFLAGS) -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) $(MAIN)
  ```
- `backend/Dockerfile`：builder 阶段**内部**重声明 `ARG VERSION=dev`（ARG 作用域仅限所在阶段，顶部 REGISTRY/GO_VERSION 的声明不延续），build 命令改为 `-ldflags="-s -w -X rivus-agent-backend/internal/buildinfo.Version=${VERSION}"`

### 3.3 release workflow

- 新增 `.github/workflows/release.yml`：
  - 触发：`on: push: tags: ['v*']`
  - `permissions: contents: write`
  - 单 job（ubuntu-latest）：checkout → setup-go 1.27 → `cd backend && go test ./...` → 顺序构建 `dist/agentd-linux-amd64`（GOARCH=amd64）与 `dist/agentd-linux-arm64`（GOARCH=arm64，CGO_ENABLED=0），VERSION=${GITHUB_REF_NAME} → `gh release create "$GITHUB_REF_NAME" dist/* --generate-notes`，env `GH_TOKEN: ${{ github.token }}`
- 不动 ci.yml（发布与 CI 职责分离）

### 3.4 文档

- `docs/api/system.md` + `docs/en/api/system.md`：开头句加 `/version` 免认证；新增 `## GET /version` 小节（响应示例 + curl）；Notes 加一条「/version 免认证，返回构建注入的版本三元组，未注入时为 dev/unknown/unknown」
- `docs/api/overview.md` + `docs/en/api/overview.md`：L16 免认证句补 /version
- `README.md` / `README.en.md`：API 表加 `| GET | /version | 构建版本信息 / Build version info |`（20→21 行）
- `backend/README.md` / `backend/README.en.md`：新增「发布（Release）」小节：`make build VERSION=v1.2.3` 本地注入；push tag `v*` 触发 release workflow（测试 → 双架构产物 → GitHub Release --generate-notes）

## 4. 文件清单

| 动作 | 文件 |
|---|---|
| 新增 | `backend/internal/buildinfo/buildinfo.go`、`backend/internal/buildinfo/buildinfo_test.go`、`backend/internal/api/http/server_version_test.go`、`.github/workflows/release.yml` |
| 修改 | `backend/internal/api/http/server.go`、`backend/Makefile`、`backend/Dockerfile`、`README.md`、`README.en.md`、`backend/README.md`、`backend/README.en.md`、`docs/api/system.md`、`docs/en/api/system.md`、`docs/api/overview.md`、`docs/en/api/overview.md` |

## 5. 测试与验收

1. 单测（TDD，先 RED 后 GREEN）：
   - `buildinfo_test.go`：默认值三元组 = dev/unknown/unknown
   - `server_version_test.go`：`/version` 无 token → 200；3 键存在；4 个安全头齐全（验证中间件链穿透）
2. 真实构建验收：`make build VERSION=v7-test` → 启动二进制（AGENT_ADDR=:18099 临时端口 + dummy 模型变量）→ `curl /version` 断言 `version=v7-test`、commit 非 unknown、build_date 非空 → 清理
3. `python3 -c yaml.safe_load(release.yml)` 解析通过；grep 关键键（tags/permissions/gh release）
4. 全量回归：`go build ./... && go vet ./... && go test ./... -count=1`；`make test-eval`（15 场景）；前端 `npx tsc --noEmit && npx vitest run`（23 文件/141 测试，零改动）
5. 路由计数：README zh/en 各 21 行 `| GET|POST|PUT|DELETE |`；server.go 21 条 HandleFunc

## 6. YAGNI（本轮不做）

- docker-compose 传递 VERSION 构建参数
- 前端版本端点/展示
- changelog 手工维护（--generate-notes 足够）
- 多 OS 矩阵（仅 linux amd64+arm64）、SBOM、镜像推送
- server.go 拆分（结构治理另轮）

## 7. 风险与对策

| 风险 | 对策 |
|---|---|
| Dockerfile ARG 作用域：顶部 ARG 在 builder 阶段不可见 | 在 builder 阶段内部重声明 ARG VERSION |
| 本地无 docker daemon / 无 act | Dockerfile 与 release.yml 仅做静态校验（grep/yaml parse），真实构建验证以 make build 代替；局限如实报告 |
| /version 信息暴露 | 仅版本/commit/时间，无密钥；与 healthz 同级免认证是运维惯例 |
| gh release create 失败（权限） | workflow 内 `permissions: contents: write` + `github.token`；无法本地验证，标注 |
