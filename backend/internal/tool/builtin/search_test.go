package builtin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSearchDir(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		fp := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fp, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("docs/guide.md", "line1\nhas needle here\nline3")
	write("notes.txt", "needle at top\nno match\nanother needle line")
	write("binary.bin", "ok\x00binary\x00needle")
	write("skip/huge.txt", "needle in huge file" + string(make([]byte, 600*1024)))
	write("plain.txt", "no relevant words here")

	hits := searchDir(root, "needle", 10)
	if len(hits) != 3 { // guide.md×1 + notes.txt×2; binary + huge skipped
		t.Fatalf("want 3 hits, got %d: %+v", len(hits), hits)
	}
	// sorted walk order: binary skipped, docs/guide.md, notes.txt (needle at top; another needle line), plain no, skip skipped
	if hits[0].Path != "docs/guide.md" || hits[0].Line != 2 {
		t.Fatalf("first hit wrong: %+v", hits[0])
	}
	if hits[1].Path != "notes.txt" || hits[1].Line != 1 {
		t.Fatalf("second hit wrong: %+v", hits[1])
	}
	if hits[2].Path != "notes.txt" || hits[2].Line != 3 {
		t.Fatalf("third hit wrong: %+v", hits[2])
	}
	for _, h := range hits {
		if h.Snippet == "" || len(h.Snippet) > 200 {
			t.Fatalf("snippet bad: %+v", h)
		}
	}

	// maxResults caps
	hits = searchDir(root, "needle", 2)
	if len(hits) != 2 {
		t.Fatalf("maxResults cap failed: got %d", len(hits))
	}

	// missing dir → no hits, no panic
	if hits := searchDir(filepath.Join(root, "nope"), "x", 5); len(hits) != 0 {
		t.Fatalf("missing dir should yield no hits, got %v", hits)
	}

	// sub-scope: search within docs/ only
	hits = searchDir(filepath.Join(root, "docs"), "needle", 10)
	if len(hits) != 1 || hits[0].Path != "guide.md" {
		t.Fatalf("scoped search wrong: %+v", hits)
	}
}
