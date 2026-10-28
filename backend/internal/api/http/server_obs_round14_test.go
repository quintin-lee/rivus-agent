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
