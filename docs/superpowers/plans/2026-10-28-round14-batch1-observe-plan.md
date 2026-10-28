# Round 14 批1 实现计划：可观测性补全（pprof + Prometheus 暴露）

> **For agentic workers:** REQUIRED: Use superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `/metrics/prometheus` 文本暴露（零依赖，遍历 expvar）+ 条件注册的 auth 保护 `/debug/pprof/`。

**Architecture:** 复用现有中间件链（securityHeaders→requestID→requestLog→auth→rateLimit→mux）；pprof 仅当 `cfg.AuthToken != ""` 时注册（auth 关闭即本地开发，不暴露剖析端点，安全默认）；Prometheus 转换器 type-switch 遍历 `expvar.Do`，跳过 String/Func/JSON（memstats/cmdline），Map 展开为 `{key="..."}` 标签。

**Tech Stack:** Go 标准库 `expvar` / `net/http/pprof` / `net/http/httptest`；无新依赖。

**Spec:** docs/superpowers/specs/2026-10-28-round14-observe-evals-frontend-design.md §3.1

---

## 已验证事实

- `newBareServer(cfg config.Config) *Server` 助手在 server_security_test.go:11（`New(cfg, nil×6)`）。
- Server struct（server.go:23-36）：加 `pprofEnabled bool`；New() 里 `s.pprofEnabled = cfg.AuthToken != ""`。
- routes.go 29 行：`/metrics` 用 `s.mux.Handle("/metrics", expvar.Handler())`；新增路由加在其后。
- auth 中间件豁免仅 `/healthz` `/readyz` `/version`（middleware.go:15-16）→ /metrics* 与 /debug/* 默认需 Bearer；rateLimit 只拦 `/api/v1/` 前缀（middleware.go:51）→ 两条新路由不受限流影响。
- metrics.go 已注册 expvar：`runs_started`、`runs_succeeded`、`runs_failed`、`tool_calls_total`(Map)、`tool_errors_total`(Map)、`event_append_failed`、`latency_ms_{0_50,50_100,100_200,200_500,500_plus}`、`rate_limited_total`。expvar 默认注册表还有 `cmdline`(String) 与 `memstats`(Func) → 转换器必须跳过非数值类型。
- `pprof.Index` 同时服务索引页与具名 profile（/debug/pprof/heap 等），cmdline/profile/symbol/trace 需独立注册。
- Harness 断言 `Contains`（不锁精确行集）防 expvar 全局状态跨测试污染。

---

### Task 1: Prometheus 文本暴露

**Files:**
- Create: `backend/internal/api/http/metrics_prometheus.go`
- Modify: `backend/internal/api/http/routes.go:28`（其后追加路由）
- Test: `backend/internal/api/http/server_obs_round14_test.go`（新建）

- [ ] **Step 1: 写失败测试**

```go
package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rivus-agent-backend/internal/config"
	"rivus-agent-backend/internal/observability"
)

func TestPrometheusExposition(t *testing.T) {
	observability.IncrRateLimited() // 确保至少一个标量非零（幂等：只增）
	srv := newBareServer(config.Config{AuthToken: "tok"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics/prometheus", nil)
	req.Header.Set("Authorization", "Bearer tok")
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/plain") || !strings.Contains(ct, "version=0.0.4") {
		t.Fatalf("content-type = %q", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"# TYPE rate_limited_total counter",
		"rate_limited_total ",
		"# TYPE runs_started counter",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q;\n%s", want, body)
		}
	}
	if strings.Contains(body, "memstats") || strings.Contains(body, "cmdline") {
		t.Fatalf("non-numeric expvars leaked:\n%s", body)
	}
}

func TestPrometheusRequiresAuth(t *testing.T) {
	srv := newBareServer(config.Config{AuthToken: "tok"})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics/prometheus", nil))
	if rec.Code != 401 {
		t.Fatalf("code = %d, want 401", rec.Code)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd backend && go test ./internal/api/http/ -run 'TestPrometheus' -count=1`
Expected: FAIL（404，路由不存在）

- [ ] **Step 3: 实现 metrics_prometheus.go**

```go
package httpapi

import (
	"expvar"
	"fmt"
	"net/http"
	"strings"
)

// handlePromMetrics 以 Prometheus 文本格式暴露 expvar 指标。
// 顶层 Int/Float 输出为 counter；Map 按 key 展开为标签；其余类型
// （String/Func/JSON，如默认注册表中的 cmdline/memstats）跳过。
func (s *Server) handlePromMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	var b strings.Builder
	expvar.Do(func(kv expvar.KeyValue) {
		switch v := kv.Value.(type) {
		case *expvar.Int:
			fmt.Fprintf(&b, "# TYPE %s counter\n%s %d\n", kv.Key, kv.Key, v.Value())
		case *expvar.Float:
			fmt.Fprintf(&b, "# TYPE %s counter\n%s %v\n", kv.Key, kv.Key, v.Value())
		case *expvar.Map:
			v.Do(func(e expvar.KeyValue) {
				fmt.Fprintf(&b, "# TYPE %s counter\n%s{key=%q} ", kv.Key, kv.Key, e.Key)
				switch ev := e.Value.(type) {
				case *expvar.Int:
					fmt.Fprintf(&b, "%d\n", ev.Value())
				case *expvar.Float:
					fmt.Fprintf(&b, "%v\n", ev.Value())
				default:
					b.WriteString("0\n")
				}
			})
		}
	})
	_, _ = w.Write([]byte(b.String()))
}
```

- [ ] **Step 4: routes.go 追加路由**（`/metrics` 行后，imports 加 `net/http`）

```go
	s.mux.HandleFunc("GET /metrics/prometheus", s.handlePromMetrics)
```

- [ ] **Step 5: 运行确认通过**

Run: `cd backend && go test ./internal/api/http/ -run 'TestPrometheus' -count=1 -v`
Expected: 2 tests PASS；`go vet ./internal/api/http/` 干净；`gofmt -l internal/api/http/` 空。

- [ ] **Step 6: Commit**

```bash
git add backend/internal/api/http/metrics_prometheus.go backend/internal/api/http/routes.go backend/internal/api/http/server_obs_round14_test.go
git commit -m "feat(http): prometheus text exposition at /metrics/prometheus (zero-dep expvar converter)"
```

---

### Task 2: pprof 条件注册（auth 开启才暴露）

**Files:**
- Modify: `backend/internal/api/http/server.go`（Server struct + New()）
- Modify: `backend/internal/api/http/routes.go`（条件注册块）
- Test: `backend/internal/api/http/server_obs_round14_test.go`（追加）

- [ ] **Step 1: 追加失败测试**

```go
func TestPprofGatedByAuth(t *testing.T) {
	// auth 开启：必须带 token，且端点已注册
	srv := newBareServer(config.Config{AuthToken: "tok"})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	if rec.Code != 401 {
		t.Fatalf("no token: code = %d, want 401", rec.Code)
	}
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	req.Header.Set("Authorization", "Bearer tok")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("with token: code = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "pprof") {
		t.Fatalf("index page unexpected:\n%s", rec.Body.String())
	}
	// heap profile（debug=1 文本，即时返回，避免 CPU profile 阻塞）
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/debug/pprof/heap?debug=1", nil)
	req.Header.Set("Authorization", "Bearer tok")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "heap profile") {
		t.Fatalf("heap: code = %d", rec.Code)
	}

	// auth 关闭（本地开发）：端点不注册 → 404
	srv2 := newBareServer(config.Config{})
	rec2 := httptest.NewRecorder()
	srv2.Handler().ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	if rec2.Code != 404 {
		t.Fatalf("auth disabled: code = %d, want 404", rec2.Code)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd backend && go test ./internal/api/http/ -run TestPprofGatedByAuth -count=1`
Expected: FAIL（auth 开启无 token 得 404 而非 401 —— 端点未注册）

- [ ] **Step 3: 实现**

server.go：Server struct 加字段（mux 行后）

```go
	pprofEnabled bool
```

New() 中 `s.routes()` 调用前加：

```go
	s.pprofEnabled = cfg.AuthToken != ""
```

routes.go：imports 加 `net/http/pprof`；`/metrics` 路由后加：

```go
	if s.pprofEnabled {
		s.mux.HandleFunc("GET /debug/pprof/", pprof.Index)
		s.mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
		s.mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
		s.mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
		s.mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
	}
```

- [ ] **Step 4: 运行确认通过 + 回归**

Run: `cd backend && go test ./internal/api/http/ -count=1 && go build ./... && go vet ./... && gofmt -l internal/api/http/`
Expected: 全部 PASS（含 R6 的 6 个 security 测试与 R9 的 observe 测试）；vet/gofmt 干净。

- [ ] **Step 5: Commit**

```bash
git add backend/internal/api/http/server.go backend/internal/api/http/routes.go backend/internal/api/http/server_obs_round14_test.go
git commit -m "feat(http): auth-gated /debug/pprof registration (only when bearer auth enabled)"
```

---

### Task 3: 批1 验收（真机 smoke + docs）

- [ ] **Step 1: 全量回归**

Run: `cd backend && go test ./... -count=1 -timeout 600s`
Expected: 全部包 ok。

- [ ] **Step 2: 真机 smoke**

```bash
cd backend && make build VERSION=v14-test
AGENT_ADDR=:18099 AGENT_DB_PATH=/tmp/rivus-r14-smoke.db AGENT_AUTH_TOKEN=sekrit \
  MODEL_BASE_URL=http://127.0.0.1:9 MODEL_API_KEY=dummy MODEL_NAME=dummy ./bin/agentd & PID=$!
sleep 1.5
curl -s localhost:18099/debug/pprof/ -H "Authorization: Bearer sekrit" | grep -c pprof   # ≥1
curl -s -o /dev/null -w '%{http_code}' localhost:18099/debug/pprof/                       # 401
curl -s localhost:18099/metrics/prometheus -H "Authorization: Bearer sekrit" | grep -c '# TYPE'  # ≥1
kill $PID; rm -f /tmp/rivus-r14-smoke.db backend/bin/agentd
```

Expected: 三个数值 ≥1 / 401 / ≥1。

- [ ] **Step 3: docs zh/en**

backend/README.md `## 观测（Observability）` 章节追加小节「pprof 与 Prometheus」；README.en.md `## Observability` 追加镜像段落：
- `/debug/pprof/`：仅当配置了 `AGENT_AUTH_TOKEN` 时注册，需 Bearer；heap/goroutine/profile 用法一行示例。
- `/metrics/prometheus`：Prometheus 文本格式（counter + {key=} 标签），auth 同 /metrics。

Run: `grep -c "metrics/prometheus" backend/README.md backend/README.en.md` → 1/1。

- [ ] **Step 4: Commit**

```bash
git add backend/README.md backend/README.en.md
git commit -m "docs(observability): pprof and prometheus exposition notes (zh/en)"
```
