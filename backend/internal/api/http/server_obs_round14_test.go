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
