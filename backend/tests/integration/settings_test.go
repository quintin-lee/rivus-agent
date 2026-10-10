package integration

import (
	"strings"
	"testing"

	"rivus-agent-backend/tests/testutil"
)

func TestModelSettingsRoundTrip(t *testing.T) {
	h := testutil.New(t)
	code, body := h.Do("GET", "/api/v1/settings/model", nil, nil)
	if code != 200 || !strings.Contains(string(body), "api_key_set") {
		t.Fatalf("GET: %d %s", code, body)
	}
	if strings.Contains(string(body), "sk-") {
		t.Fatal("api key must never be serialized")
	}
	code, body = h.Do("PUT", "/api/v1/settings/model",
		map[string]string{"base_url": "https://x.test/v1", "model": "m1", "api_key": "sk-test"}, nil)
	if code != 200 {
		t.Fatalf("PUT: %d %s", code, body)
	}
	_, body2 := h.Do("GET", "/api/v1/settings/model", nil, nil)
	for _, want := range []string{`"model":"m1"`, `"api_key_set":true`} {
		if !strings.Contains(string(body2), want) {
			t.Fatalf("missing %s in %s", want, body2)
		}
	}
	h.Do("PUT", "/api/v1/settings/model", map[string]string{"model": "m2"}, nil)
	_, body3 := h.Do("GET", "/api/v1/settings/model", nil, nil)
	if !strings.Contains(string(body3), `"api_key_set":true`) {
		t.Fatalf("empty api_key must keep existing key: %s", body3)
	}
	code, _ = h.Do("PUT", "/api/v1/settings/model", map[string]string{"provider": "nope"}, nil)
	if code != 400 {
		t.Fatalf("invalid provider must 400, got %d", code)
	}
}
