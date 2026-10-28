package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mcpJSONServer answers initialize / tools/list / tools/call with plain JSON.
func mcpJSONServer(t *testing.T, gotHeaders *map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotHeaders = map[string]string{
			"Authorization": r.Header.Get("Authorization"),
			"Accept":        r.Header.Get("Accept"),
		}
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2024-11-05"}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{"name": "echo", "description": "d"}}}
		case "tools/call":
			result = map[string]any{"content": []map[string]any{{"type": "text", "text": "pong"}}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
}

func TestHTTPTransportJSONRoundtrip(t *testing.T) {
	var got map[string]string
	srv := mcpJSONServer(t, &got)
	defer srv.Close()
	tr, err := NewHTTPTransport(context.Background(), ServerSpec{Name: "r", Type: "http", URL: srv.URL,
		Headers: map[string]string{"Authorization": "Bearer t0k"}})
	if err != nil {
		t.Fatal(err)
	}
	c := NewClient(tr)
	if err := c.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if got["Authorization"] != "Bearer t0k" || !strings.Contains(got["Accept"], "text/event-stream") {
		t.Fatalf("headers=%v", got)
	}
	tools, err := c.ListTools(context.Background())
	if err != nil || len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools=%v err=%v", tools, err)
	}
	out, err := c.Call(context.Background(), "echo", nil)
	if err != nil || out != "pong" {
		t.Fatalf("call=%q err=%v", out, err)
	}
}

func TestHTTPTransportSSEResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int `json:"id"`
			Method string
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = w.Write([]byte(": keepalive\n\n"))
		flusher.Flush()
		payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID,
			"result": map[string]any{"content": []map[string]any{{"type": "text", "text": "sse-pong"}}}})
		_, _ = w.Write([]byte("event: message\ndata: " + string(payload) + "\n\n"))
		flusher.Flush()
	}))
	defer srv.Close()
	tr, _ := NewHTTPTransport(context.Background(), ServerSpec{Name: "r", Type: "http", URL: srv.URL})
	out, err := NewClient(tr).Call(context.Background(), "echo", nil)
	if err != nil || out != "sse-pong" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestHTTPTransportErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "denied", http.StatusUnauthorized)
	}))
	defer srv.Close()
	tr, _ := NewHTTPTransport(context.Background(), ServerSpec{Name: "r", Type: "http", URL: srv.URL})
	if err := NewClient(tr).Initialize(context.Background()); err == nil {
		t.Fatal("401 must fail")
	}
}

func TestHTTPTransportReadWithoutWrite(t *testing.T) {
	tr, _ := newHTTPTransport(&http.Client{}, ServerSpec{Name: "r", Type: "http", URL: "http://127.0.0.1:1"})
	if _, err := tr.ReadLine(); err == nil {
		t.Fatal("read before write must fail")
	}
}
