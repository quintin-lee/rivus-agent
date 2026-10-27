package httpapi

import (
	"crypto/subtle"
	"math"
	"net/http"
	"strconv"
	"strings"

	"rivus-agent-backend/internal/observability"
)

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/healthz") || strings.HasPrefix(r.URL.Path, "/readyz") ||
			strings.HasPrefix(r.URL.Path, "/version") {
			next.ServeHTTP(w, r)
			return
		}
		if s.cfg.AuthToken != "" {
			token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok || subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.AuthToken)) != 1 {
				writeErr(w, http.StatusUnauthorized, "unauthorized", "missing or invalid token", false)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

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
