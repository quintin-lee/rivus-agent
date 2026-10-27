package httpapi

import (
	"encoding/json"
	"net/http"
)

func ownerOf(r *http.Request) string {
	if v := r.Header.Get("X-Owner-ID"); v != "" {
		return v
	}
	return "default"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string, retryable bool) {
	writeJSON(w, status, map[string]any{"code": code, "message": msg, "retryable": retryable})
}

func parseAfter(r *http.Request) int64 {
	if v := r.URL.Query().Get("after"); v != "" {
		var n int64
		for _, c := range v {
			if c < '0' || c > '9' {
				n = 0
				break
			}
			n = n*10 + int64(c-'0')
		}
		return n
	}
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		var n int64
		for _, c := range v {
			if c < '0' || c > '9' {
				return 0
			}
			n = n*10 + int64(c-'0')
		}
		return n
	}
	return 0
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	b := []byte{}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
