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

func setupSkillWithRefs(t *testing.T) (*Loader, string) {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("demo/SKILL.md", "# demo\ndescription: does X\nversion: 1\n\nSKILL BODY here\n")
	write("demo/references/api.md", "API REFERENCE")
	l := NewLoader(dir)
	if err := l.Refresh(); err != nil {
		t.Fatal(err)
	}
	return l, dir
}

func TestInvokeSkillReferenceIndexAndInput(t *testing.T) {
	l, _ := setupSkillWithRefs(t)
	def := InvokeSkill(l, 0)

	// 默认：body + 引用索引
	out, err := def.Handler(context.Background(), json.RawMessage(`{"name":"demo"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "SKILL BODY here") || !strings.Contains(out, "## References") || !strings.Contains(out, "api.md") {
		t.Fatalf("default output missing body/index: %q", out)
	}

	// input 注入：body 之后追加 ## 运行时输入 JSON 块
	out, err = def.Handler(context.Background(), json.RawMessage(`{"name":"demo","input":{"k":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "## 运行时输入") || !strings.Contains(out, `"k":1`) {
		t.Fatalf("input block missing: %q", out)
	}
	// input 非对象 → 报错
	if _, err := def.Handler(context.Background(), json.RawMessage(`{"name":"demo","input":"str"}`)); err == nil {
		t.Fatal("non-object input must error")
	}
}

func TestInvokeSkillReferenceLoad(t *testing.T) {
	l, _ := setupSkillWithRefs(t)
	def := InvokeSkill(l, 0)

	out, err := def.Handler(context.Background(), json.RawMessage(`{"name":"demo","reference":"api.md"}`))
	if err != nil || out != "API REFERENCE" {
		t.Fatalf("reference load = %q, %v", out, err)
	}
	if _, err := def.Handler(context.Background(), json.RawMessage(`{"name":"demo","reference":"../SKILL.md"}`)); err == nil {
		t.Fatal("reference escape must error")
	}
}
