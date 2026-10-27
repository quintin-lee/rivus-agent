# Round-9 批1：HTTP 可观测性（requestID + 请求日志）实现计划

> **For agentic workers:** REQUIRED: Use superpowers:executing-plans（本会话无 subagent，用户已约束）。步骤使用 `- [ ]` 复选框。

**Goal:** HTTP 层补上 X-Request-ID 中间件与结构化请求日志，401/429/404 错误路径同样可观测。

**Architecture:** 单文件 `observe.go` 两个 Server 方法中间件；链序 `securityHeaders(requestID(requestLog(auth(rateLimit(mux)))))`（日志捕获取最终 status，安全头最外层不变，auth 在 rateLimit 外保持 401 先于 429）；logger 经既有 Option 模式注入（`WithLogger`，nil → `slog.Default()`）。

**Tech Stack:** Go 1.27、log/slog、crypto/rand、net/http；无新依赖。

**Spec:** docs/superpowers/specs/2026-10-27-round9-observability-evals-history-design.md §3.1

---

## 已核实事实（写码前逐条确认过）

- middleware.go：auth exempts `/healthz` `/readyz` `/version`；securityHeaders 设 4 头；rateLimit 仅 `/api/v1/` 前缀、rpm<=0 直通。
- server.go:120 `Handler()` 现为 `s.securityHeaders(s.auth(s.rateLimit(s.mux)))`。
- `Server` struct 无 log 字段；`serverOptions{tools,hub}` + `Option` 模式在 server.go:82-108；`New()` L100-112 构造并调 `s.routes()`。
- app.go:124 `httpapi.New(cfg, db, tasks, events, settings, runs, sessions, httpapi.WithToolConfigProvider(provider), httpapi.WithEventHub(hub))`；`a.log` 已存在（app.go:31）。
- 测试基建：`server_security_test.go` 有 `newBareServer()` 助手（`New(config.Config{}, nil×6)`）；nil services 只要不触达真 handler 就安全 → 观测测试用 `/healthz`（无依赖）、`/api/v1/nope`（401 走 auth，不触 mux handler）、`/metrics`（expvar）。
- Go `http.Header.Set` 对含 CR/LF 的值会在写响应时报错 → 必须在 Set 前净化用户传入的 ID。

---

### Task 1: observe.go + 链接线 + WithLogger + app.go（TDD，单任务单提交）

**Files:**
- Create: `backend/internal/api/http/observe.go`
- Create: `backend/internal/api/http/observe_test.go`
- Modify: `backend/internal/api/http/server.go`（Server.log 字段 + serverOptions.log + WithLogger + New 默认值 + Handler() 链）
- Modify: `backend/internal/app/app.go:124`（加 `httpapi.WithLogger(a.log)`）

- [ ] **Step 1.1 写失败测试** `observe_test.go`（package httpapi）：

```go
package httpapi

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 日志捕获助手：带自定义 logger 的裸 Server。
func newObservedServer(buf *bytes.Buffer) *Server {
	h := slog.NewTextHandler(buf, nil)
	return New(config{}, nil, nil, nil, nil, nil, nil, WithLogger(slog.New(h)))
}

func TestRequestIDGenerated(t *testing.T) {
	srv := newObservedServer(&bytes.Buffer{})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	id := rec.Header().Get("X-Request-ID")
	if len(id) != 32 {
		t.Fatalf("X-Request-ID len = %d, want 32 hex chars, got %q", len(id), id)
	}
	// 两次请求 ID 不同
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec2.Header().Get("X-Request-ID") == id {
		t.Fatal("two requests must produce different request IDs")
	}
}

func TestRequestIDPreserved(t *testing.T) {
	srv := newObservedServer(&bytes.Buffer{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("X-Request-ID", "my-trace-123")
	srv.Handler().ServeHTTP(rec, req)
	if got := rec.Header().Get("X-Request-ID"); got != "my-trace-123" {
		t.Fatalf("X-Request-ID = %q, want echo of caller value", got)
	}
}

func TestRequestIDSanitized(t *testing.T) {
	srv := newObservedServer(&bytes.Buffer{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("X-Request-ID", "evil\r\nX-Injected: yes")
	srv.Handler().ServeHTTP(rec, req)
	got := rec.Header().Get("X-Request-ID")
	if strings.ContainsAny(got, "\r\n: ") {
		t.Fatalf("X-Request-ID must be sanitized, got %q", got)
	}
}

func TestRequestLogFields(t *testing.T) {
	var buf bytes.Buffer
	srv := newObservedServer(&buf)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	line := buf.String()
	for _, want := range []string{"msg=http request", "method=GET", "path=/healthz", "status=200", "request_id="} {
		if !strings.Contains(line, want) {
			t.Fatalf("log line missing %q; got %q", want, line)
		}
	}
}

func TestRequestLogAndIDOnErrorPaths(t *testing.T) {
	var buf bytes.Buffer
	srv := newObservedServer(&buf)
	// 401：无 token 访问受保护路径（auth 在 requestLog 内层 → 401 也被记录并带 ID）
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/nope", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if rec.Header().Get("X-Request-ID") == "" {
		t.Fatal("401 response must carry X-Request-ID")
	}
	if !strings.Contains(buf.String(), "status=401") {
		t.Fatalf("log must capture 401; got %q", buf.String())
	}
}
```

注意：`config{}` 应为 `config.Config{}`（import `rivus-agent-backend/internal/config`）——写码时直接写全名。

- [ ] **Step 1.2 跑测试确认 RED**

```bash
cd backend && go test ./internal/api/http/ -run 'TestRequestID|TestRequestLog' -count=1 2>&1 | head -20
```
预期：编译失败 `undefined: WithLogger`（及 requestID/requestLog 未定义则随后暴露）。

- [ ] **Step 1.3 实现** `observe.go`：

```go
package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// requestID 为每个请求确定追踪 ID：沿用调用方 X-Request-ID（净化 + 截断 64），
// 否则生成 16 字节 hex；写入响应头并放入 context。
func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := sanitizeRequestID(r.Header.Get("X-Request-ID"))
		if id == "" {
			id = genRequestID()
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKeyRequestID{}, id)))
	})
}

type ctxKeyRequestID struct{}

// RequestIDFrom 返回 requestID 中间件放入 context 的追踪 ID。
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(ctxKeyRequestID{}).(string)
	return id
}

// sanitizeRequestID 只保留安全字符（字母数字 - _ .），截断 64，防响应头注入。
func sanitizeRequestID(v string) string {
	var b strings.Builder
	for _, c := range v {
		if len(b.String()) >= 64 {
			break
		}
		if c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-' || c == '_' || c == '.' {
			b.WriteRune(c)
		}
	}
	return b.String()
}

func genRequestID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand 失败极其罕见；退化为时间戳，保证响应头始终有值。
		return hex.EncodeToString([]byte(time.Now().Format("150405.000000000")))[:32]
	}
	return hex.EncodeToString(buf[:])
}

// statusRecorder 捕获响应状态码（默认 200）。
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// requestLog 在请求结束后输出一条结构化访问日志（含 401/429 等错误路径）。
func (s *Server) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.log.Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"request_id", RequestIDFrom(r.Context()),
		)
	})
}
```

- [ ] **Step 1.4 server.go 接线**（4 处小改）：

1. `Server` struct 加字段：`log *slog.Logger`（import `log/slog`）。
2. `serverOptions` 加 `log *slog.Logger`；新增：

```go
func WithLogger(l *slog.Logger) Option {
	return func(o *serverOptions) { o.log = l }
}
```

3. `New()` 构造行后加：

```go
s.log = o.log
if s.log == nil {
	s.log = slog.Default()
}
```

4. `Handler()` 改为：

```go
return s.securityHeaders(s.requestID(s.requestLog(s.auth(s.rateLimit(s.mux)))))
```

- [ ] **Step 1.5 app.go 接线**：L124 `httpapi.New(...)` 第二行选项追加 `httpapi.WithLogger(a.log)`。

- [ ] **Step 1.6 跑测试确认 GREEN + 全包回归 + 格式**

```bash
cd backend && go build ./... && go vet ./... && go test ./internal/api/http/ -count=1 && gofmt -l internal/api/http/ internal/app/
```
预期：全部通过（含 round-6 的 6 个安全测试、TestVersionEndpoint）、gofmt 无输出。

- [ ] **Step 1.7 后端全量回归**

```bash
cd backend && go test ./... -count=1 -timeout 600s
```
预期：全部 ok，exit 0。

- [ ] **Step 1.8 提交**

```bash
git add backend/internal/api/http/observe.go backend/internal/api/http/observe_test.go backend/internal/api/http/server.go backend/internal/app/app.go
git commit -m "feat(http): X-Request-ID middleware and structured request logging"
```

---

### Task 2: 批1 验收 + 文档

- [ ] **Step 2.1 验证验收 §4.4**：observe_test.go 已覆盖 401 带 ID + 日志 status=401（Task 1 完成）；再补 429 路径冒烟（复用 server_security_test 的模式不在本轮扩测 —— rateLimit 中间件在 requestLog 内层，429 天然被记录；由 TestRateLimit429AndHealthzExempt 已锁行为，日志断言由 401 用例代表）。
- [ ] **Step 2.2 文档**：backend/README.md `## 观测（Observability）` 段（`## 说明` 前）追加一句：每个 HTTP 响应带 `X-Request-ID`（可沿用调用方值），请求结束输出结构化访问日志（method/path/status/duration_ms/request_id）；README.en.md 同步英文。
- [ ] **Step 2.3 前端零漂移验证**

```bash
cd frontend && npx tsc --noEmit && npx vitest run
```
预期：tsc 0 错误；23 文件/144 测试全过（本批零前端改动）。

- [ ] **Step 2.4 提交**

```bash
git add backend/README.md backend/README.en.md
git commit -m "docs(observability): request id and access log notes (zh/en)"
```

---

## 自审记录

- 链序风险：requestID 在 requestLog 外层 → context 值在 requestLog 读取时已就位 ✓；securityHeaders 最外层与 round-6 修正一致 ✓。
- `sanitizeRequestID` 中 `b.String()` 在循环内调用为 O(n²) —— 但上限 64 字符，实际开销可忽略；如在意可改长度计数器（实现时用计数器）。
- `statusRecorder` 不处理 Flush/Hijack（SSE 走 Flush？检查：handlers_runs.go handleEvents 用 Flusher —— 需要透传 Flush！**修正：statusRecorder 增加 `Flush()` 方法透传**，否则 SSE 心跳失效。实现时加入：

```go
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
```

- genRequestID 退化分支长度：`time.Now().Format(...)` 18 字节 hex 36 字符 → 截 32 ✓（写码时核对）。
- nil-services 测试安全性：/healthz 与 /api/v1/nope(401) 均不触达 mux handler ✓。
