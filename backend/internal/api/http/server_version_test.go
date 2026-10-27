package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rivus-agent-backend/internal/config"
)

func TestVersionEndpoint(t *testing.T) {
	srv := newBareServer(config.Config{})

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
