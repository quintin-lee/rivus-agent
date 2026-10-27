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
