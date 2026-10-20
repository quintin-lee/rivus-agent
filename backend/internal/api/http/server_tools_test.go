package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rivus-agent-backend/internal/config"
)

type stubToolsProvider struct {
	status ToolStatus
	saved  []ToolConfigIn
}

func (s *stubToolsProvider) GetToolsStatus(ctx context.Context) (ToolStatus, error) {
	return s.status, nil
}

func (s *stubToolsProvider) SaveTools(ctx context.Context, in ToolConfigIn) error {
	s.saved = append(s.saved, in)
	return nil
}

func jsonReader(b []byte) *strings.Reader {
	return strings.NewReader(string(b))
}

func TestToolsEndpoints(t *testing.T) {
	stub := &stubToolsProvider{status: ToolStatus{
		Skills: SkillsStatus{Enabled: true, Available: []SkillInfo{{Name: "demo", Description: "d"}}},
		MCP:    MCPStatus{Enabled: true, Servers: []MCPServerInfo{{Name: "fs", Status: "up", Command: "npx"}}},
	}}
	srv := New(config.Config{}, nil, nil, nil, nil, nil, nil, WithToolConfigProvider(stub))

	// GET /settings/tools → 200 + the fixed ToolStatus
	rec := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "http://x/api/v1/settings/tools", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET tools status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var got ToolStatus
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Skills.Enabled || len(got.MCP.Servers) != 1 || got.MCP.Servers[0].Name != "fs" {
		t.Fatalf("GET tools round-trip wrong: %+v", got)
	}

	// PUT /settings/tools → 200 + the stub recorded the payload
	in := ToolConfigIn{SkillsEnabled: false, MCPEnabled: true, MCPServers: []MCPServerIn{{Name: "git", Command: "git-mcp", Args: []string{"stdio"}}}}
	b, _ := json.Marshal(in)
	rec2 := httptest.NewRecorder()
	req2, _ := http.NewRequest(http.MethodPut, "http://x/api/v1/settings/tools", jsonReader(b))
	srv.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("PUT tools status = %d, want 200: %s", rec2.Code, rec2.Body.String())
	}
	if len(stub.saved) != 1 || stub.saved[0].MCPEnabled != true || len(stub.saved[0].MCPServers) != 1 {
		t.Fatalf("PUT payload not recorded as expected: %+v", stub.saved)
	}
}

// TestToolsEndpointsNoProvider: a bare 7-arg New (no option) → GET returns 501.
func TestToolsEndpointsNoProvider(t *testing.T) {
	srv := New(config.Config{}, nil, nil, nil, nil, nil, nil) // 7-arg, no option
	rec := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "http://x/api/v1/settings/tools", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("GET tools with no provider = %d, want 501: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "tools_not_configured" {
		t.Fatalf("501 code = %q, want tools_not_configured", body.Code)
	}
}
