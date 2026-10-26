package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetDirsInjectsRoots(t *testing.T) {
	ws := t.TempDir()
	kn := t.TempDir()
	SetDirs(ws, kn)

	// workspace_write + file_read land in injected workspace root
	wOut, err := workspaceWrite(context.Background(), json.RawMessage(`{"path":"a.txt","content":"hello"}`))
	if err != nil {
		t.Fatalf("workspaceWrite: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, "a.txt")); err != nil {
		t.Fatalf("file not in injected workspace root: %v (out=%s)", err, wOut)
	}
	rOut, err := fileRead(context.Background(), json.RawMessage(`{"path":"a.txt"}`))
	if err != nil {
		t.Fatalf("fileRead: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(rOut), &got); err != nil {
		t.Fatalf("fileRead out not JSON: %v (%s)", err, rOut)
	}
	if got["path"] != "a.txt" || got["content"] != "hello" {
		t.Fatalf("fileRead out = %s, want path=a.txt content=hello", rOut)
	}

	// knowledgeRoot var is set by the hook
	if knowledgeRoot != kn {
		t.Fatalf("knowledgeRoot = %q, want %q", knowledgeRoot, kn)
	}
	if workspaceRoot != ws {
		t.Fatalf("workspaceRoot = %q, want %q", workspaceRoot, ws)
	}
}

func TestFileListAndFileSearch(t *testing.T) {
	ws := t.TempDir()
	SetDirs(ws, t.TempDir())
	write := func(rel, content string) {
		fp := filepath.Join(ws, rel)
		os.MkdirAll(filepath.Dir(fp), 0o755)
		os.WriteFile(fp, []byte(content), 0o644)
	}
	write("docs/guide.md", "has needle here")
	write("notes.txt", "plain")
	write("skip.bin", "x")

	// file_list recursive with pattern
	out, err := fileList(context.Background(), json.RawMessage(`{"pattern":"*.md"}`))
	if err != nil {
		t.Fatalf("fileList: %v", err)
	}
	var fl struct {
		Entries []struct {
			Name  string `json:"name"`
			IsDir bool   `json:"is_dir"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(out), &fl); err != nil {
		t.Fatalf("fileList out: %v (%s)", err, out)
	}
	if len(fl.Entries) != 1 || fl.Entries[0].Name != filepath.Join("docs", "guide.md") {
		t.Fatalf("fileList entries wrong: %s", out)
	}

	// file_list plain dir listing
	out, err = fileList(context.Background(), json.RawMessage(`{"path":"docs"}`))
	if err != nil {
		t.Fatalf("fileList dir: %v", err)
	}
	if !strings.Contains(out, "guide.md") {
		t.Fatalf("fileList dir listing missing entry: %s", out)
	}

	// file_list traversal rejected
	if _, err := fileList(context.Background(), json.RawMessage(`{"path":"../outside"}`)); err == nil {
		t.Fatal("fileList traversal not rejected")
	}

	// file_search
	out, err = fileSearch(context.Background(), json.RawMessage(`{"query":"needle"}`))
	if err != nil {
		t.Fatalf("fileSearch: %v", err)
	}
	var fsRes struct {
		Hits []searchHit `json:"hits"`
	}
	if err := json.Unmarshal([]byte(out), &fsRes); err != nil {
		t.Fatalf("fileSearch out: %v (%s)", err, out)
	}
	if len(fsRes.Hits) != 1 || fsRes.Hits[0].Path != filepath.Join("docs", "guide.md") || fsRes.Hits[0].Line != 1 {
		t.Fatalf("fileSearch hits wrong: %s", out)
	}

	// file_search empty query rejected
	if _, err := fileSearch(context.Background(), json.RawMessage(`{"query":"  "}`)); err == nil {
		t.Fatal("fileSearch empty query not rejected")
	}
}

func TestFileEdit(t *testing.T) {
	ws := t.TempDir()
	SetDirs(ws, t.TempDir())
	os.WriteFile(filepath.Join(ws, "code.txt"), []byte("alpha\nbeta\ngamma\nbeta-end\n"), 0o644)

	// unique occurrence → replace succeeds
	out, err := fileEdit(context.Background(), json.RawMessage(`{"path":"code.txt","old_text":"beta\n","new_text":"BETA\n"}`))
	if err != nil {
		t.Fatalf("fileEdit: %v", err)
	}
	var res map[string]any
	json.Unmarshal([]byte(out), &res)
	if res["path"] != "code.txt" {
		t.Fatalf("fileEdit out: %s", out)
	}
	b, _ := os.ReadFile(filepath.Join(ws, "code.txt"))
	if string(b) != "alpha\nBETA\ngamma\nbeta-end\n" {
		t.Fatalf("file after edit: %q", string(b))
	}

	// zero occurrences → error, file unchanged
	if _, err := fileEdit(context.Background(), json.RawMessage(`{"path":"code.txt","old_text":"missing","new_text":"x"}`)); err == nil {
		t.Fatal("zero-occurrence edit not rejected")
	}
	// two occurrences → error, file unchanged
	os.WriteFile(filepath.Join(ws, "two.txt"), []byte("dup dup"), 0o644)
	if _, err := fileEdit(context.Background(), json.RawMessage(`{"path":"two.txt","old_text":"dup","new_text":"x"}`)); err == nil {
		t.Fatal("ambiguous edit not rejected")
	}
	b, _ = os.ReadFile(filepath.Join(ws, "two.txt"))
	if string(b) != "dup dup" {
		t.Fatalf("ambiguous edit mutated file: %q", string(b))
	}
	// missing file → error
	if _, err := fileEdit(context.Background(), json.RawMessage(`{"path":"nope.txt","old_text":"a","new_text":"b"}`)); err == nil {
		t.Fatal("missing-file edit not rejected")
	}
	// traversal rejected
	if _, err := fileEdit(context.Background(), json.RawMessage(`{"path":"../x","old_text":"a","new_text":"b"}`)); err == nil {
		t.Fatal("traversal not rejected")
	}
}
