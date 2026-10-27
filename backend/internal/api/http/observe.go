package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
)

// requestID 为每个请求确定追踪 ID：沿用调用方 X-Request-ID（净化 + 截断 64），
// 否则生成 16 字节 hex；写入响应头并放入 context（RequestIDFrom 可读取）。
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

// RequestIDFrom 返回 requestID 中间件放入 context 的追踪 ID（未经过中间件时为空串）。
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(ctxKeyRequestID{}).(string)
	return id
}

// sanitizeRequestID 只保留安全字符（字母数字 - _ .），截断 64，防响应头注入。
func sanitizeRequestID(v string) string {
	var b strings.Builder
	n := 0
	for _, c := range v {
		if n >= 64 {
			break
		}
		if c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-' || c == '_' || c == '.' {
			b.WriteRune(c)
			n++
		}
	}
	return b.String()
}

func genRequestID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand 失败极其罕见；退化为高精度时间戳，保证响应头始终有值。
		fallback := hex.EncodeToString([]byte(time.Now().Format("150405.000000000")))
		return fallback[:32]
	}
	return hex.EncodeToString(buf[:])
}

// statusRecorder 捕获响应状态码（默认 200）；透传 Flush 以保 SSE 心跳正常。
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Flush 透传底层 Flusher（handlers_runs 的 SSE 事件流依赖它）。
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
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
