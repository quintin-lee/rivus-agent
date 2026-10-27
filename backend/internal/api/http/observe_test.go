package httpapi

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rivus-agent-backend/internal/config"
)

// newObservedServer 构造带自定义 logger 的裸 Server（日志捕获用）；
// AuthToken 置非空以便 401 路径断言（auth 空串 = 关闭校验）。
func newObservedServer(buf *bytes.Buffer) *Server {
	h := slog.NewTextHandler(buf, nil)
	return New(config.Config{AuthToken: "test-token"}, nil, nil, nil, nil, nil, nil, WithLogger(slog.New(h)))
}

func TestRequestIDGenerated(t *testing.T) {
	srv := newObservedServer(&bytes.Buffer{})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	id := rec.Header().Get("X-Request-ID")
	if len(id) != 32 {
		t.Fatalf("X-Request-ID len = %d, want 32 hex chars, got %q", len(id), id)
	}
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
	for _, want := range []string{"msg=", "http request", "method=GET", "path=/healthz", "status=200", "request_id="} {
		if !strings.Contains(line, want) {
			t.Fatalf("log line missing %q; got %q", want, line)
		}
	}
}

func TestRequestLogAndIDOnErrorPaths(t *testing.T) {
	var buf bytes.Buffer
	srv := newObservedServer(&buf)
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
