package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func rlRequest(key string) *http.Request {
	r := httptest.NewRequest("GET", "/api/v1/sessions", nil)
	if key != "" {
		r.Header.Set("X-Owner-ID", key)
	}
	return r
}

func TestRateLimiterDisabled(t *testing.T) {
	rl := newRateLimiter(0)
	for i := 0; i < 100; i++ {
		if ok, _ := rl.allow(rlRequest("a")); !ok {
			t.Fatalf("disabled limiter denied request %d", i)
		}
	}
}

func TestRateLimiterDeniesAfterBurst(t *testing.T) {
	rl := newRateLimiter(2)
	if ok, _ := rl.allow(rlRequest("a")); !ok {
		t.Fatal("first request must pass")
	}
	if ok, _ := rl.allow(rlRequest("a")); !ok {
		t.Fatal("second request must pass")
	}
	ok, wait := rl.allow(rlRequest("a"))
	if ok {
		t.Fatal("third rapid request must be denied")
	}
	if wait <= 0 {
		t.Fatalf("denied request must carry positive retry window, got %v", wait)
	}
}

func TestRateLimiterKeysSeparated(t *testing.T) {
	rl := newRateLimiter(1)
	r1 := httptest.NewRequest("GET", "/api/v1/sessions", nil)
	r1.Header.Set("X-Owner-ID", "alice")
	r2 := httptest.NewRequest("GET", "/api/v1/sessions", nil)
	r2.Header.Set("X-Owner-ID", "bob")
	if ok, _ := rl.allow(r1); !ok {
		t.Fatal("alice first must pass")
	}
	if ok, _ := rl.allow(r1); ok {
		t.Fatal("alice second must be denied")
	}
	if ok, _ := rl.allow(r2); !ok {
		t.Fatal("bob must be independent of alice")
	}
}

func TestRateLimiterEvictionCap(t *testing.T) {
	rl := newRateLimiter(10000)
	for i := 0; i < 1100; i++ {
		r := httptest.NewRequest("GET", "/api/v1/sessions", nil)
		r.Header.Set("X-Owner-ID", fmt.Sprintf("u%04d", i))
		_, _ = rl.allow(r)
	}
	rl.mu.Lock()
	n := len(rl.clients)
	rl.mu.Unlock()
	if n > 1024 {
		t.Fatalf("client table must be capped at 1024, got %d", n)
	}
}
