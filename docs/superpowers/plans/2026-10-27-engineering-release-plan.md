# 工程化深化实施计划 — buildinfo + /version + ldflags + release workflow (Round 7)

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking. 本项目约束：**不使用 subagents**（用户指示），直接在 master 提交，TDD 每任务先 RED 后 GREEN。

**Goal:** 二进制与镜像注入版本三元组，暴露免认证 `GET /version`，打 tag `v*` 自动构建双架构产物并发 GitHub Release。

**Architecture:** 独立 `internal/buildinfo` 包承载 ldflags 注入变量（零接线）；http server 加一条路由 + auth 豁免前缀；Makefile/Dockerfile 构建命令追加 `-ldflags -X`；release.yml 单 job 顺序构建 amd64/arm64 后 `gh release create`。

**Tech Stack:** Go 1.27（无新依赖）、GitHub Actions（actions/checkout@v4、actions/setup-go@v5、gh CLI）、GNU Make、Docker 多阶段构建。

**Spec:** `docs/superpowers/specs/2026-10-27-engineering-release-design.md`（commit 24893c4）

---

**执行注意（重要）**：
- bash 工具的 `workdir` 每次调用独立，`go`/`make` 命令必须带 `workdir: backend`（或绝对路径）
- httpapi 测试包已有 `newBareServer` helper（`server_security_test.go`，round 6 引入），Task 2 直接复用
- 现有 20 条 HandleFunc 路由；README API 表 zh 末行 L138 `/metrics`、en 末行 L139 `/metrics`

---

## Chunk 1: 版本注入与端点

### Task 1: buildinfo 包（TDD）

**Files:**
- Create: `backend/internal/buildinfo/buildinfo.go`
- Test: `backend/internal/buildinfo/buildinfo_test.go`

- [ ] **Step 1: 写失败测试**

创建 `backend/internal/buildinfo/buildinfo_test.go`：

```go
package buildinfo

import "testing"

func TestDefaults(t *testing.T) {
	if Version != "dev" {
		t.Errorf("Version default = %q, want dev", Version)
	}
	if Commit != "unknown" {
		t.Errorf("Commit default = %q, want unknown", Commit)
	}
	if BuildDate != "unknown" {
		t.Errorf("BuildDate default = %q, want unknown", BuildDate)
	}
}
```

- [ ] **Step 2: 运行确认 RED**

Run: `cd backend && go test ./internal/buildinfo/ -v`
Expected: FAIL —— `buildinfo` 包不存在，编译错误 `no Go files` / `undefined: Version`

- [ ] **Step 3: 最小实现**

创建 `backend/internal/buildinfo/buildinfo.go`：

```go
// Package buildinfo 承载构建时经 -ldflags "-X" 注入的版本信息。
// 默认值为本地源码运行（未注入）时的兜底。
package buildinfo

var (
	// Version 版本号；发布构建注入 git tag（如 v1.2.3）。
	Version = "dev"
	// Commit 构建 commit 短哈希。
	Commit = "unknown"
	// BuildDate UTC 构建时间（RFC3339）。
	BuildDate = "unknown"
)
```

- [ ] **Step 4: 运行确认 GREEN**

Run: `cd backend && go test ./internal/buildinfo/ -v`
Expected: PASS（1 test）

- [ ] **Step 5: 提交**

```bash
git add backend/internal/buildinfo/
git commit -m "feat(build): buildinfo package as ldflags injection target"
```

### Task 2: GET /version 端点 + auth 豁免（TDD）

**Files:**
- Modify: `backend/internal/api/http/server.go`（路由注册 ~L152 后、auth 豁免 ~L175、handleReady 后加 handler）
- Test: `backend/internal/api/http/server_version_test.go`（新建）

- [ ] **Step 1: 写失败测试**

创建 `backend/internal/api/http/server_version_test.go`：

```go
package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVersionEndpoint(t *testing.T) {
	srv := newBareServer()

	// 1) 无 token 可访问（免认证）
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/version", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /version without token: code = %d, want 200", rec.Code)
	}

	body := rec.Body.String()
	for _, key := range []string{"version", "commit", "build_date"} {
		if !strings.Contains(body, `"`+key+`"`) {
			t.Errorf("GET /version body missing key %q: %s", key, body)
		}
	}

	// 2) 安全头穿透（headers 中间件在最外层）
	for _, h := range []string{"X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy", "Cache-Control"} {
		if rec.Header().Get(h) == "" {
			t.Errorf("GET /version missing security header %s", h)
		}
	}
}
```

注意：`newBareServer` 签名以 `server_security_test.go` 中实际定义为准（若带参数，按现签名补参）。

- [ ] **Step 2: 运行确认 RED**

Run: `cd backend && go test ./internal/api/http/ -run TestVersionEndpoint -v`
Expected: FAIL —— `code = 404`（路由不存在）

- [ ] **Step 3: 实现**

`backend/internal/api/http/server.go` 三处修改：

(a) import 增加 `"rivus-agent-backend/internal/buildinfo"`；

(b) 路由注册（`GET /readyz` 行 L152 之后）：

```go
	s.mux.HandleFunc("GET /version", s.handleVersion)
```

(c) auth 豁免（L175 附近前缀判断追加）：

```go
		if strings.HasPrefix(r.URL.Path, "/healthz") || strings.HasPrefix(r.URL.Path, "/readyz") ||
			strings.HasPrefix(r.URL.Path, "/version") {
```

(d) `handleReady` 之后加 handler：

```go
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]string{
		"version":    buildinfo.Version,
		"commit":     buildinfo.Commit,
		"build_date": buildinfo.BuildDate,
	})
}
```

- [ ] **Step 4: 运行确认 GREEN**

Run: `cd backend && go test ./internal/api/http/ -count=1 && go build ./... && go vet ./...`
Expected: 全部 PASS/OK（含 round-6 既有 6 个安全测试）

- [ ] **Step 5: 提交**

```bash
git add backend/internal/api/http/server.go backend/internal/api/http/server_version_test.go
git commit -m "feat(http): auth-exempt GET /version serving injected build info"
```

### Task 3: Makefile + Dockerfile 构建注入

**Files:**
- Modify: `backend/Makefile`（头部变量 + build 目标）
- Modify: `backend/Dockerfile`（builder 阶段 ARG + ldflags）

- [ ] **Step 1: 修改 Makefile**

`backend/Makefile` 头部（`GOFLAGS ?=` 之后）追加：

```make
VERSION ?= dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo unknown)
LDFLAGS := -X $(MODULE)/internal/buildinfo.Version=$(VERSION) -X $(MODULE)/internal/buildinfo.Commit=$(COMMIT) -X $(MODULE)/internal/buildinfo.BuildDate=$(DATE)
```

build 目标改为：

```make
build:
	mkdir -p bin
	go build $(GOFLAGS) -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) $(MAIN)
```

- [ ] **Step 2: 修改 Dockerfile**

`backend/Dockerfile` builder 阶段（`WORKDIR /src` 之后、`RUN go build` 之前）插入：

```dockerfile
# 阶段内重声明：ARG 作用域仅限所在阶段，顶部声明不延续
ARG VERSION=dev
```

build 命令改为：

```dockerfile
RUN go build -trimpath -ldflags="-s -w -X rivus-agent-backend/internal/buildinfo.Version=${VERSION}" -o /out/agentd ./cmd/agentd
```

- [ ] **Step 3: 真实构建验收（RED→GREEN 性质：改前 make build 产物 /version 为 dev）**

Run:
```bash
cd backend && make build VERSION=v7-test
AGENT_ADDR=:18099 AGENT_DB_PATH=/tmp/rivus-v7-test.db MODEL_BASE_URL=http://127.0.0.1:9 MODEL_API_KEY=dummy MODEL_NAME=dummy ./bin/agentd &
sleep 1.5
curl -s localhost:18099/version
kill %1; rm -f /tmp/rivus-v7-test.db
```
Expected: `{"version":"v7-test","commit":"<短哈希>","build_date":"<UTC时间>"}` —— version 非 dev、commit 非 unknown

- [ ] **Step 4: Dockerfile 静态校验（本地无 docker daemon，如实报告局限）**

Run:
```bash
grep -c "internal/buildinfo" backend/Dockerfile   # ≥1
grep -n "ARG VERSION" backend/Dockerfile          # 在 builder 阶段（FROM 之后）
```

- [ ] **Step 5: 提交**

```bash
git add backend/Makefile backend/Dockerfile
git commit -m "feat(build): inject version triple via ldflags in make and docker builds"
```

---

## Chunk 2: 发布流水线与文档

### Task 4: release.yml + 文档同步 + 全量验收

**Files:**
- Create: `.github/workflows/release.yml`
- Modify: `docs/api/system.md`、`docs/en/api/system.md`、`docs/api/overview.md`、`docs/en/api/overview.md`、`README.md:138` 后、`README.en.md:139` 后、`backend/README.md`、`backend/README.en.md`

- [ ] **Step 1: 创建 release workflow**

`.github/workflows/release.yml`：

```yaml
name: release

on:
  push:
    tags: ['v*']

permissions:
  contents: write

jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version: '1.27'

      - name: Test
        working-directory: backend
        run: go test ./... -count=1

      - name: Build (linux/amd64 + linux/arm64)
        working-directory: backend
        run: |
          mkdir -p dist
          CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
            -ldflags "-X rivus-agent-backend/internal/buildinfo.Version=${GITHUB_REF_NAME}" \
            -o dist/agentd-linux-amd64 ./cmd/agentd
          CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath \
            -ldflags "-X rivus-agent-backend/internal/buildinfo.Version=${GITHUB_REF_NAME}" \
            -o dist/agentd-linux-arm64 ./cmd/agentd

      - name: Create Release
        env:
          GH_TOKEN: ${{ github.token }}
        run: gh release create "$GITHUB_REF_NAME" backend/dist/* --generate-notes
```

- [ ] **Step 2: 校验 workflow**

Run: `python3 -c "import yaml;yaml.safe_load(open('.github/workflows/release.yml'));print('YAML_OK')"`
Expected: `YAML_OK`；另 grep `tags`、`contents: write`、`gh release create` 各 ≥1

- [ ] **Step 3: 文档同步**

1. `docs/api/system.md`：L3 句改为「`/healthz`、`/readyz`、`/version` **免认证**」；`## GET /metrics` 小节后新增：

```markdown
## GET /version

构建版本信息（免认证）。三元组经 `-ldflags -X` 注入，未注入时为 `dev` / `unknown` / `unknown`。

```bash
curl localhost:8080/version
```

```json
{"version":"v1.2.3","commit":"47839d7","build_date":"2026-10-27T03:00:00Z"}
```
```

注意事项列表加一条：`- /version 返回构建注入的版本三元组；本地 `make build VERSION=v1.2.3` 可注入`

2. `docs/en/api/system.md`：同样结构（auth-exempt 句 + `## GET /version` 小节 + Notes 条目）
3. `docs/api/overview.md` L16 与 `docs/en/api/overview.md` L16：免认证列举补 `/version`
4. `README.md:138` 后加：`| GET | \`/version\` | 构建版本信息（免认证） |`；`README.en.md:139` 后加：`| GET | \`/version\` | Build version info (auth-exempt) |`
5. `backend/README.md` 末尾「## 说明」前新增：

```markdown
## 发布（Release）

- 本地注入：`make build VERSION=v1.2.3`（Commit/BuildDate 自动取当前 git 与 UTC 时间）
- 远端发布：push tag `v*` 触发 `release` workflow——跑全量测试 → 构建 linux amd64/arm64 二进制 → `gh release create --generate-notes`
- 运行时核对：`GET /version`（免认证）返回版本三元组
```

6. `backend/README.en.md` 对应英文小节

- [ ] **Step 4: 全量验收**

```bash
cd backend && go build ./... && go vet ./... && go test ./... -count=1 -timeout 600s
make test-eval          # 15 场景 pass_rate 1
cd ../frontend && npx tsc --noEmit && npx vitest run   # 23 文件/141 测试
grep -cE '^\| (GET|POST|PUT|DELETE) \|' README.md README.en.md   # 各 21
```
Expected: 全绿；路由计数 21/21

- [ ] **Step 5: 提交**

```bash
git add .github/workflows/release.yml docs/ README.md README.en.md backend/README.md backend/README.en.md
git commit -m "feat(release): tag-triggered release workflow + /version docs (zh/en)"
```

---

## 验收标准（对齐 spec §5）

1. buildinfo 默认值测试 + /version 端点测试 GREEN
2. `make build VERSION=v7-test` → 真实二进制 curl `/version` 三字段正确
3. release.yml YAML 可解析 + 关键键齐全
4. go build/vet/test 全绿 + make test-eval 15 场景 + 前端 tsc 0 错误 / vitest 141 全过
5. README zh/en API 表各 21 行；system.md/overview.md zh/en 补 /version
