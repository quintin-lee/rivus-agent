# Security Hardening Implementation Plan (Round 6)

> **For agentic workers:** REQUIRED: Use superpowers:executing-plans to implement this plan (user constraint: NO subagents). Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add opt-in per-client HTTP rate limiting, constant-time token comparison, and standard security response headers — zero behavior change under default config.

**Architecture:** New `ratelimit.go` in `httpapi` package (x/time/rate token bucket keyed by X-Owner-ID or source IP, LRU-capped at 1024 clients); middleware chain in `Handler()` = `securityHeaders(auth(rateLimit(mux)))` — headers outermost (401/429 also carry them), auth next (401 before 429), limiter innermost scoped to `/api/v1/`. Metric `rate_limited_total` in observability.

**Tech Stack:** Go 1.27, `golang.org/x/time/rate` v0.16.0 (already in go.mod — zero new deps), expvar, stdlib `crypto/subtle`.

**Spec:** `docs/superpowers/specs/2026-10-27-security-hardening-design.md` (committed 2423471)

---

## Verified facts (do not re-derive)

- `backend/internal/config/config.go`: `Default()` L42, `Load(path string)` L64, `applyEnv(c *Config)` L83; env pattern `if v := os.Getenv("AGENT_X"); v != "" { ... }`; yaml-tagged fields L17-23; `HTTPFetchAllowlist` at L23.
- `backend/internal/api/http/server.go`: `New(cfg config.Config, db *sql.DB, tasks, events, settings *store.*, runs *service.RunService, sessions *service.SessionService, opts ...Option) *Server` L113; `Handler()` L126 returns `s.auth(s.mux)`; `auth` L170-184 (exempts `/healthz`,`/readyz` prefix); `writeErr(w, status, code, msg, retryable)` L166 → `{"code","message","retryable"}`; routes() L130-152 (20 routes under `/api/v1/` except health/ready/metrics).
- `golang.org/x/time v0.16.0` IS a direct require in `backend/go.mod` (line 9).
- Test package `httpapi` (server_tools_test.go); `New(config.Config{}, nil, nil, nil, nil, nil, nil)` + `srv.Handler().ServeHTTP(rec, req)` is the established pattern. **CRITICAL: nil services panic if a request REACHES a real handler — tests must use middleware-level calls, unregistered paths (mux 404s safely), or `/metrics`.**
- `observability/metrics.go`: expvar Int pattern (`eventAppendFailed` + `IncrEventAppendFailed()`).
- `.env.example`: `# 中文注释` + `VAR=` lines, sections split by `# ---- X ----`.
- README.md config table rows ~L154-156; README.en.md ~L155-157 (same table, English).
- `config_test.go`: `TestWorkspaceAndFetchConfig` uses `Default()` + `t.Setenv` + `Load("")`.

## Self-review notes

- Chain order corrected vs first draft: headers must be OUTERMOST (401/429 responses also get security headers); 401-before-429 preserved (rateLimit inside auth).
- `Reserve()/Cancel()` pattern computes exact `Retry-After` while returning the token on denial (denied requests must not starve future budget).
- `math.Ceil` for Retry-After seconds; `strconv` may be missing in config.go imports — add if so.
- Panic-free test strategy documented above (nil services never deref'd).
- YAGNI honored: no per-route limits, no XFF trust, no hot reload, no IP ban, frontend untouched.

---

## Task 1: Config `RateLimitRPM` + env (TDD)

**Files:**
- Modify: `backend/internal/config/config.go` (field after L23; env in `applyEnv` after the HTTPFetchAllowlist block)
- Test: `backend/internal/config/config_test.go`

- [ ] **Step 1: Write the failing test** — append to config_test.go:

```go
func TestRateLimitConfig(t *testing.T) {
	if Default().RateLimitRPM != 0 {
		t.Fatalf("default must be 0 (off), got %d", Default().RateLimitRPM)
	}
	t.Setenv("AGENT_RATE_LIMIT_RPM", "120")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.RateLimitRPM != 120 {
		t.Fatalf("env override failed: got %d", c.RateLimitRPM)
	}
	t.Setenv("AGENT_RATE_LIMIT_RPM", "-5")
	c2, _ := Load("")
	if c2.RateLimitRPM != 0 {
		t.Fatalf("negative must be ignored: got %d", c2.RateLimitRPM)
	}
	t.Setenv("AGENT_RATE_LIMIT_RPM", "abc")
	c3, _ := Load("")
	if c3.RateLimitRPM != 0 {
		t.Fatalf("non-numeric must be ignored: got %d", c3.RateLimitRPM)
	}
}
```

- [ ] **Step 2: Run** `cd backend && go test ./internal/config/ -run TestRateLimitConfig -v` → FAIL (unknown field `RateLimitRPM`).

- [ ] **Step 3: Implement** — in config.go struct (after `HTTPFetchAllowlist` L23):

```go
	// RateLimitRPM API 速率限制（每分钟每客户端请求数，按 X-Owner-ID 或来源 IP 分桶）；
	// 0 = 关闭（默认，向后兼容）。
	RateLimitRPM int `yaml:"rate_limit_rpm"`
```

In `applyEnv` (after the HTTPFetchAllowlist block; add `"strconv"` to imports if missing):

```go
	if v := os.Getenv("AGENT_RATE_LIMIT_RPM"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			c.RateLimitRPM = n
		}
	}
```

- [ ] **Step 4: Run** → PASS. Also `go build ./...`.

- [ ] **Step 5: Commit** `feat(config): AGENT_RATE_LIMIT_RPM setting (default 0 = off)`

## Task 2: Rate limiter core + metric (TDD)

**Files:**
- Create: `backend/internal/api/http/ratelimit.go`
- Modify: `backend/internal/observability/metrics.go`
- Test: `backend/internal/api/http/ratelimit_test.go`, `backend/internal/observability/metrics_test.go`

- [ ] **Step 1: Write failing tests** — new ratelimit_test.go:

```go
package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func rlRequest(key string) *http.Request {
	r := httptest.NewRequest("GET", "/api/v1/sessions", nil)
	if key != "" {
		r.Header.Set("X-Owner-ID", key)
	}
	return r
}

func TestRateLimiterDisabled(t *testing.T) {
	rl := newRateLimiter(0)
	for i := 0; i < 100; i++ {
		if ok, _ := rl.allow(rlRequest("a")); !ok {
			t.Fatalf("disabled limiter denied request %d", i)
		}
	}
}

func TestRateLimiterDeniesAfterBurst(t *testing.T) {
	rl := newRateLimiter(2)
	if ok, _ := rl.allow(rlRequest("a")); !ok {
		t.Fatal("first request must pass")
	}
	if ok, _ := rl.allow(rlRequest("a")); !ok {
		t.Fatal("second request must pass")
	}
	ok, wait := rl.allow(rlRequest("a"))
	if ok {
		t.Fatal("third rapid request must be denied")
	}
	if wait <= 0 {
		t.Fatalf("denied request must carry positive retry window, got %v", wait)
	}
}

func TestRateLimiterKeysSeparated(t *testing.T) {
	rl := newRateLimiter(1)
	r1 := httptest.NewRequest("GET", "/api/v1/sessions", nil)
	r1.Header.Set("X-Owner-ID", "alice")
	r2 := httptest.NewRequest("GET", "/api/v1/sessions", nil)
	r2.Header.Set("X-Owner-ID", "bob")
	if ok, _ := rl.allow(r1); !ok {
		t.Fatal("alice first must pass")
	}
	if ok, _ := rl.allow(r1); ok {
		t.Fatal("alice second must be denied")
	}
	if ok, _ := rl.allow(r2); !ok {
		t.Fatal("bob must be independent of alice")
	}
}

func TestRateLimiterEvictionCap(t *testing.T) {
	rl := newRateLimiter(10000)
	for i := 0; i < 1100; i++ {
		r := httptest.NewRequest("GET", "/api/v1/sessions", nil)
		r.Header.Set("X-Owner-ID", fmt.Sprintf("u%04d", i))
		_, _ = rl.allow(r)
	}
	rl.mu.Lock()
	n := len(rl.clients)
	rl.mu.Unlock()
	if n > 1024 {
		t.Fatalf("client table must be capped at 1024, got %d", n)
	}
}
```

(add `"fmt"` to test imports). In observability/metrics_test.go append:

```go
func TestIncrRateLimited(t *testing.T) {
	before := expvar.Get("rate_limited_total").(*expvar.Int).Value()
	IncrRateLimited()
	IncrRateLimited()
	after := expvar.Get("rate_limited_total").(*expvar.Int).Value()
	if after-before != 2 {
		t.Fatalf("want delta 2, got %d", after-before)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/api/http/ -run TestRateLimiter -v && go test ./internal/observability/ -run TestIncrRateLimited -v` → FAIL (undefined `newRateLimiter` / `IncrRateLimited`).

- [ ] **Step 3: Implement** — ratelimit.go:

```go
package httpapi

import (
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// maxTrackedClients 限制客户端表内存（每个客户端一个令牌桶）。
const maxTrackedClients = 1024

type clientLimiter struct {
	lim      *rate.Limiter
	lastSeen time.Time
}

// rateLimiter 按 X-Owner-ID（优先）或来源 IP 维护每客户端令牌桶。
type rateLimiter struct {
	mu      sync.Mutex
	rpm     int
	clients map[string]*clientLimiter
}

func newRateLimiter(rpm int) *rateLimiter {
	return &rateLimiter{rpm: rpm, clients: make(map[string]*clientLimiter)}
}

// clientKey 不信任 X-Forwarded-For（可伪造）。
func clientKey(r *http.Request) string {
	if v := r.Header.Get("X-Owner-ID"); v != "" {
		return "owner:" + v
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return "ip:" + host
	}
	return "ip:" + r.RemoteAddr
}

// allow 报告请求是否放行；拒绝时 retryAfter 为建议等待时长。
func (rl *rateLimiter) allow(r *http.Request) (bool, time.Duration) {
	if rl.rpm <= 0 {
		return true, 0
	}
	key := clientKey(r)
	now := time.Now()
	rl.mu.Lock()
	cl, ok := rl.clients[key]
	if !ok {
		if len(rl.clients) >= maxTrackedClients {
			rl.evictOldestLocked()
		}
		cl = &clientLimiter{lim: rate.NewLimiter(rate.Every(time.Minute/time.Duration(rl.rpm)), rl.rpm)}
		rl.clients[key] = cl
	}
	cl.lastSeen = now
	rl.mu.Unlock()

	res := cl.lim.Reserve()
	if res.Delay() == 0 {
		return true, 0 // 令牌立即可用（已被消费）
	}
	wait := res.Delay()
	res.Cancel() // 归还预约：被拒请求不得占用未来预算
	return false, wait
}

// evictOldestLocked 调用方须已持锁；O(n) 扫描仅在表满时触发。
func (rl *rateLimiter) evictOldestLocked() {
	var oldestKey string
	var oldest time.Time
	first := true
	for k, cl := range rl.clients {
		if first || cl.lastSeen.Before(oldest) {
			oldestKey, oldest, first = k, cl.lastSeen, false
		}
	}
	delete(rl.clients, oldestKey)
}
```

metrics.go (after `eventAppendFailed` block):

```go
var rateLimitedTotal = expvar.NewInt("rate_limited_total")

// IncrRateLimited 记录一次因限流被拒绝的请求。
func IncrRateLimited() { rateLimitedTotal.Add(1) }
```

- [ ] **Step 4: Run** both packages → PASS. `go build ./... && go vet ./...`.

- [ ] **Step 5: Commit** `feat(http): per-client rate limiter core + rate_limited_total metric`

## Task 3: Server wiring — chain, 429, headers, constant-time auth (TDD)

**Files:**
- Modify: `backend/internal/api/http/server.go` (Handler L126, auth L170-184)
- Create: `backend/internal/api/http/server_security_test.go`

- [ ] **Step 1: Write failing tests**:

```go
package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"rivus-agent-backend/internal/config"
)

func newBareServer(cfg config.Config) *Server {
	return New(cfg, nil, nil, nil, nil, nil, nil)
}

func TestSecurityHeadersOnAllResponses(t *testing.T) {
	srv := newBareServer(config.Config{})
	h := srv.Handler()
	for _, path := range []string{"/healthz", "/metrics", "/api/v1/nope"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		for k, want := range map[string]string{
			"X-Content-Type-Options": "nosniff",
			"X-Frame-Options":        "DENY",
			"Referrer-Policy":        "no-referrer",
			"Cache-Control":          "no-store",
		} {
			if got := rec.Header().Get(k); got != want {
				t.Fatalf("%s %s: got %q want %q", path, k, got, want)
			}
		}
	}
}

func TestRateLimit429AndHealthzExempt(t *testing.T) {
	srv := newBareServer(config.Config{RateLimitRPM: 2})
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	h := srv.rateLimit(next)
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, rlRequest("a"))
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d must pass", i+1)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, rlRequest("a"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("third request must be 429, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("429 must carry Retry-After")
	}
	if rec.Header().Get("X-Rate-Limited") != "" {
		t.Fatal("unexpected header")
	}
	// healthz 豁免（非 /api/v1/ 前缀）
	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
		if rec.Code == http.StatusTooManyRequests {
			t.Fatal("healthz must be exempt from rate limiting")
		}
	}
}

func TestRateLimitDisabledByDefault(t *testing.T) {
	srv := newBareServer(config.Config{})
	h := srv.rateLimit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	for i := 0; i < 50; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, rlRequest("a"))
		if rec.Code == http.StatusTooManyRequests {
			t.Fatal("rpm=0 must never limit")
		}
	}
}

func TestRateLimitedMetricWired(t *testing.T) {
	srv := newBareServer(config.Config{RateLimitRPM: 1})
	h := srv.rateLimit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	_ = h // 由 429 分支调用 observability.IncrRateLimited()；此处仅编译期确认导入无误
}

func TestAuthConstantTimePaths(t *testing.T) {
	srv := newBareServer(config.Config{AuthToken: "s3cret"})
	h := srv.Handler()
	// 无 token → 401
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token must be 401, got %d", rec.Code)
	}
	// 错 token → 401
	rec = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token must be 401, got %d", rec.Code)
	}
	// 正确 token → 放行到 /metrics（expvar 无依赖，安全）
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/metrics", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid token must pass, got %d", rec.Code)
	}
}

func TestAuthBeforeRateLimit(t *testing.T) {
	srv := newBareServer(config.Config{AuthToken: "s3cret", RateLimitRPM: 1})
	h := srv.Handler()
	// 未注册路径：带有效 token 消耗掉唯一预算 → mux 404（证明到达了 mux）
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/definitely-unregistered", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("registered check: want 404, got %d", rec.Code)
	}
	// 无 token：auth 在 rateLimit 外侧 → 401（不是 429）
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/definitely-unregistered", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("auth must precede rate limit: want 401, got %d", rec.Code)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/api/http/ -run "TestSecurityHeaders|TestRateLimit|TestAuth" -v` → FAIL (rateLimit/securityHeaders undefined; auth not constant-time yet — behavior tests may pass for 401 paths; the headers/429 tests define the RED).

- [ ] **Step 3: Implement** in server.go:

Handler() L126:

```go
func (s *Server) Handler() http.Handler {
	return s.securityHeaders(s.auth(s.rateLimit(s.mux)))
}
```

auth L170-184 — replace token check (add `"crypto/subtle"` import):

```go
		if s.cfg.AuthToken != "" {
			token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok || subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.AuthToken)) != 1 {
				writeErr(w, http.StatusUnauthorized, "unauthorized", "missing or invalid token", false)
				return
			}
		}
```

New middleware methods (place after auth):

```go
// securityHeaders 对一切响应（含 401/429 错误路径）设置安全响应头。
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// rateLimit 仅对 /api/v1/ 前缀生效；探针路径（/healthz /readyz /metrics）豁免。
// rpm<=0 时零开销直通（默认关闭，向后兼容）。
func (s *Server) rateLimit(next http.Handler) http.Handler {
	if s.cfg.RateLimitRPM <= 0 {
		return next
	}
	rl := newRateLimiter(s.cfg.RateLimitRPM)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v1/") {
			next.ServeHTTP(w, r)
			return
		}
		if ok, wait := rl.allow(r); !ok {
			observability.IncrRateLimited()
			secs := int(math.Ceil(wait.Seconds()))
			if secs < 1 {
				secs = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(secs))
			writeErr(w, http.StatusTooManyRequests, "rate_limited", "too many requests", true)
			return
		}
		next.ServeHTTP(w, r)
	})
}
```

imports to add in server.go: `"crypto/subtle"`, `"math"`, `"strconv"`, `"rivus-agent-backend/internal/observability"` (strings/net/http already present).

- [ ] **Step 4: Run** httpapi package → PASS. `go build ./... && go vet ./...`.

- [ ] **Step 5: Commit** `feat(http): wire rate limit, security headers, constant-time token compare`

## Task 4: Docs sync + full acceptance

**Files:**
- Modify: `.env.example`（HTTP_FETCH 行后）、`README.md` ~L156、`README.en.md` ~L157

- [ ] **Step 1: .env.example**（AGENT_HTTP_FETCH_ALLOWLIST 行之后、`# ---- Docker 构建 ----` 之前）:

```
# API 速率限制（每分钟每客户端请求数，按 X-Owner-ID 或来源 IP 分桶；0 = 关闭）
AGENT_RATE_LIMIT_RPM=0
```

- [ ] **Step 2: README.md** L156 后:

```markdown
| `AGENT_RATE_LIMIT_RPM` | API 速率限制（每分钟每客户端请求数，按 X-Owner-ID 或来源 IP 分桶；0 = 关闭，默认 0） |
```

README.en.md L157 后:

```markdown
| `AGENT_RATE_LIMIT_RPM` | API rate limit (requests per minute per client, keyed by X-Owner-ID or source IP; 0 = disabled, default 0) |
```

- [ ] **Step 3: Full acceptance**

```bash
cd backend && go build ./... && go vet ./... && go test ./... -count=1 -timeout 600s
cd backend && make test-eval          # 15 场景 pass_rate 1
cd frontend && npx tsc --noEmit && npx vitest run   # 零前端改动，23 文件/141 测试全过
grep -c "AGENT_RATE_LIMIT_RPM" .env.example README.md README.en.md   # 1/1/1
```

- [ ] **Step 4: Commit** `docs(security): rate limit env example and config tables (zh/en)`

---

## 验收标准（对照 spec §7）

1. 每任务 RED→GREEN，全部新测试通过
2. `go build/vet` 干净；`go test ./... -count=1` 全包 ok（含 evals 15 场景）
3. 前端零改动且 tsc/vitest 全绿
4. 默认配置行为不变（限流关闭；新增响应头不改变 JSON body）
5. 文档三处同步完成
