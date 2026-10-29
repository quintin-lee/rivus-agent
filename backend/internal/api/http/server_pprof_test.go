package httpapi

import (
	"net/http/httptest"
	"testing"

	"rivus-agent-backend/internal/config"
)

func boolPtr(b bool) *bool { return &b }

func TestPprofToggleOverridesAuthDefault(t *testing.T) {
	// 认证开 + AGENT_PPROF=false → pprof 不注册 → 404
	srv := newBareServer(config.Config{AuthToken: "tok", Pprof: boolPtr(false)})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/debug/pprof/", nil)
	req.Header.Set("Authorization", "Bearer tok")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("pprof=false with auth: code=%d, want 404", rec.Code)
	}

	// AGENT_PPROF=true 无认证 → 路由注册，auth 拦截 → 401（与 /metrics 行为一致）
	srv2 := newBareServer(config.Config{Pprof: boolPtr(true)})
	rec2 := httptest.NewRecorder()
	srv2.Handler().ServeHTTP(rec2, httptest.NewRequest("GET", "/debug/pprof/cmdline", nil))
	if rec2.Code != 200 {
		t.Fatalf("pprof=true no auth: code=%d, want 200", rec2.Code)
	}

	// nil → 默认跟随认证：无 token 404
	srv3 := newBareServer(config.Config{})
	rec3 := httptest.NewRecorder()
	srv3.Handler().ServeHTTP(rec3, httptest.NewRequest("GET", "/debug/pprof/", nil))
	if rec3.Code != 404 {
		t.Fatalf("nil default without auth: code=%d, want 404", rec3.Code)
	}
}
