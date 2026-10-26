package builtin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rivus-agent-backend/internal/tool"
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

func TestHTTPFetch(t *testing.T) {
	ws := t.TempDir()
	SetDirs(ws, t.TempDir())

	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(405)
			return
		}
		_, _ = w.Write([]byte("0123456789ABCDEFGHIJ"))
	}))
	defer srv.Close()
	srvURL = srv.URL // 127.0.0.1

	// allowlist empty → all denied
	SetHTTPAllowlist(nil)
	if _, err := httpFetch(context.Background(), json.RawMessage(`{"url":"`+srvURL+`"}`)); err == nil {
		t.Fatal("empty allowlist must deny")
	}

	// allowlist contains 127.0.0.1 → success
	SetHTTPAllowlist([]string{"127.0.0.1"})
	out, err := httpFetch(context.Background(), json.RawMessage(`{"url":"`+srvURL+`"}`))
	if err != nil {
		t.Fatalf("httpFetch: %v", err)
	}
	if !strings.Contains(out, `"status":200`) || !strings.Contains(out, "0123456789") {
		t.Fatalf("httpFetch out: %s", out)
	}

	// max_bytes truncates body
	out, err = httpFetch(context.Background(), json.RawMessage(`{"url":"`+srvURL+`","max_bytes":5}`))
	if err != nil {
		t.Fatalf("httpFetch max_bytes: %v", err)
	}
	if !strings.Contains(out, "01234") || strings.Contains(out, "0123456789A") {
		t.Fatalf("max_bytes truncation failed: %s", out)
	}

	// non-listed domain rejected BEFORE request (host exact match)
	if _, err := httpFetch(context.Background(), json.RawMessage(`{"url":"http://example.invalid/x"}`)); err == nil {
		t.Fatal("non-allowlisted host must be rejected")
	}

	// non-http scheme rejected
	if _, err := httpFetch(context.Background(), json.RawMessage(`{"url":"ftp://127.0.0.1/x"}`)); err == nil {
		t.Fatal("non-http scheme must be rejected")
	}
}

func TestKnowledgeSearchReal(t *testing.T) {
	kn := t.TempDir()
	SetDirs(t.TempDir(), kn)
	os.MkdirAll(filepath.Join(kn, "rfc"), 0o755)
	os.WriteFile(filepath.Join(kn, "rfc/notes.md"), []byte("budget enforcement details\nunrelated line\n"), 0o644)
	os.WriteFile(filepath.Join(kn, "intro.txt"), []byte("intro without keyword\n"), 0o644)

	out, err := knowledgeSearch(context.Background(), json.RawMessage(`{"query":"budget"}`))
	if err != nil {
		t.Fatalf("knowledgeSearch: %v", err)
	}
	if !strings.Contains(out, "rfc/notes.md") || !strings.Contains(out, "budget enforcement") {
		t.Fatalf("knowledgeSearch no real hits: %s", out)
	}
	if strings.Contains(out, "stub") {
		t.Fatalf("knowledgeSearch still stub: %s", out)
	}

	// empty knowledge dir → no hits, no error
	SetDirs(t.TempDir(), t.TempDir())
	out, err = knowledgeSearch(context.Background(), json.RawMessage(`{"query":"budget"}`))
	if err != nil || !strings.Contains(out, `"hits":[]`) {
		t.Fatalf("empty knowledge dir: err=%v out=%s", err, out)
	}
}

func TestWorkspaceWritePolicyWithNilScopes(t *testing.T) {
	// 回归：gateway 从不传 scopes；workspace_write 此前带 RequiredScopes 必被
	// CheckPolicy 以 missing scope 拒绝（潜在死锁）。修复后应为 NeedApproval。
	reg := tool.NewRegistry()
	if err := RegisterAll(reg); err != nil {
		t.Fatal(err)
	}
	var def *tool.Definition
	for _, d := range reg.List() {
		if d.Name == "workspace_write" {
			def = &d
			break
		}
	}
	if def == nil {
		t.Fatal("workspace_write not found")
	}
	if len(def.RequiredScopes) != 0 {
		t.Fatalf("workspace_write still has RequiredScopes: %v", def.RequiredScopes)
	}
	dec := tool.CheckPolicy(*def, nil, false)
	if !dec.NeedApproval || dec.DenyReason != "" {
		t.Fatalf("want NeedApproval with nil scopes, got %+v", dec)
	}
}
