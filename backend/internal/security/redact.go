package security

import "strings"

// Redact 对日志/事件中的敏感字段做脱敏。
func Redact(s string) string {
	if s == "" {
		return s
	}
	lower := strings.ToLower(s)
	for _, k := range []string{"api_key", "apikey", "authorization", "secret", "password", "token"} {
		if strings.Contains(lower, k) {
			return "[REDACTED]"
		}
	}
	if len(s) > 2000 {
		return s[:2000] + "...[truncated]"
	}
	return s
}

// RedactMap 对 map 中的敏感键脱敏。
func RedactMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "key") || strings.Contains(lk, "secret") || strings.Contains(lk, "token") || strings.Contains(lk, "password") {
			out[k] = "[REDACTED]"
			continue
		}
		out[k] = v
	}
	return out
}
