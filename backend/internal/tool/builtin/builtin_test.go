package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
