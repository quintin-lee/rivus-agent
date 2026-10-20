package skill

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rivus-agent-backend/internal/tool"
)

func TestInvokeSkill(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "# demo\ndescription: does X\nversion: 1\n\nSKILL BODY here\n"
	if err := os.WriteFile(filepath.Join(dir, "demo", "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	l := NewLoader(dir)
	if err := l.Refresh(); err != nil {
		t.Fatal(err)
	}

	def := InvokeSkill(l, 0)
	if def.Risk != tool.RiskRead || def.Approval != tool.ApprovalNever {
		t.Fatalf("want read/never, got %s/%s", def.Risk, def.Approval)
	}
	if len(def.RequiredScopes) != 0 {
		t.Fatalf("RequiredScopes must be empty, got %v", def.RequiredScopes)
	}

	// known skill returns the body
	out, err := def.Handler(context.Background(), json.RawMessage(`{"name":"demo"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "SKILL BODY here") {
		t.Fatalf("handler output missing body: %q", out)
	}

	// unknown skill errors
	if _, err := def.Handler(context.Background(), json.RawMessage(`{"name":"nope"}`)); err == nil {
		t.Fatal("unknown skill must error")
	}
	// empty name / invalid args errors
	if _, err := def.Handler(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("empty name must error")
	}

	// maxBytes truncation
	small := InvokeSkill(l, 5)
	out, err = small.Handler(context.Background(), json.RawMessage(`{"name":"demo"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 5 {
		t.Fatalf("truncated output len = %d, want 5 (%q)", len(out), out)
	}
}
