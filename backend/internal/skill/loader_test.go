package skill

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseMetaAllFields(t *testing.T) {
	m := parseMeta("demo", "# demo\nwhen_to_use: fix build errors\nversion: 2\ntools: file_edit, http_fetch\ndescription: does X\n\nbody")
	if m.WhenToUse != "fix build errors" || m.Version != "2" || m.Description != "does X" {
		t.Fatalf("scalars wrong: %+v", m)
	}
	if !reflect.DeepEqual(m.Tools, []string{"file_edit", "http_fetch"}) {
		t.Fatalf("tools = %v", m.Tools)
	}
}

func TestParseMetaBlankLineStops(t *testing.T) {
	// 首个空行后的正文出现 description: 不得污染元数据
	m := parseMeta("demo", "# demo\ndescription: real\nversion: 1\n\nlater description: fake\nwhen_to_use: fake")
	if m.Description != "real" || m.WhenToUse != "" {
		t.Fatalf("blank line must stop parsing: %+v", m)
	}
}

func TestParseMetaMissingFieldsZero(t *testing.T) {
	m := parseMeta("demo", "# demo\ndescription: only desc\n")
	if m.Version != "" || m.WhenToUse != "" || len(m.Tools) != 0 {
		t.Fatalf("missing fields must be zero: %+v", m)
	}
}

func TestParseMetaToolsEmptyAndSpaces(t *testing.T) {
	m := parseMeta("demo", "tools:  a , ,b \n")
	if !reflect.DeepEqual(m.Tools, []string{"a", "b"}) {
		t.Fatalf("tools = %q", m.Tools)
	}
}

func TestListAndReadReferences(t *testing.T) {
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
	write("demo/SKILL.md", "description: d\nversion: 1\n")
	write("demo/references/b.md", "REF B")
	write("demo/references/a.md", "REF A")
	write("demo/references/notes.txt", "not md")
	l := NewLoader(dir)
	if err := l.Refresh(); err != nil {
		t.Fatal(err)
	}

	refs, err := l.ListReferences("demo")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(refs, []string{"a.md", "b.md"}) {
		t.Fatalf("refs = %v (want sorted md-only)", refs)
	}

	got, err := l.ReadReference("demo", "a.md")
	if err != nil || got != "REF A" {
		t.Fatalf("read ref = %q, %v", got, err)
	}

	// 路径逃逸拒绝
	if _, err := l.ReadReference("demo", "../SKILL.md"); err == nil {
		t.Fatal("path escape must be rejected")
	}
	if _, err := l.ReadReference("demo", "/etc/passwd"); err == nil {
		t.Fatal("absolute path must be rejected")
	}
	// 未知 skill / 未知引用
	if _, err := l.ListReferences("nope"); err == nil {
		t.Fatal("unknown skill must error")
	}
	if _, err := l.ReadReference("demo", "missing.md"); err == nil {
		t.Fatal("missing reference must error")
	}
	// 无 references 目录
	write("plain/SKILL.md", "description: p\n")
	if _, err := l.ListReferences("plain"); err == nil {
		t.Fatal("missing references dir must error")
	}
}
