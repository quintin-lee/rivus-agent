package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"rivus-agent-backend/internal/tool"
)

func RegisterAll(r *tool.Registry) error {
	defs := []tool.Definition{
		{
			Name: "knowledge_search", Description: "在允许的本地知识目录中检索文档摘要（只读）", Version: "v1",
			InputSchema: `{"type":"object","properties":{"query":{"type":"string"},"top_k":{"type":"integer"}},"required":["query"]}`,
			Risk:        tool.RiskRead, Approval: tool.ApprovalNever, Idempotent: true, TimeoutMs: 10000, MaxResultBytes: 16 * 1024,
			Handler: knowledgeSearch,
		},
		{
			Name: "file_read", Description: "读取工作区内允许范围的文件（只读，禁路径穿越）", Version: "v1",
			InputSchema: `{"type":"object","properties":{"path":{"type":"string"},"max_bytes":{"type":"integer"}},"required":["path"]}`,
			Risk:        tool.RiskRead, Approval: tool.ApprovalNever, Idempotent: true, TimeoutMs: 10000, MaxResultBytes: 32 * 1024,
			Handler: fileRead,
		},
		{
			Name: "file_list", Description: "列出工作区内目录条目（只读，可选 glob 递归匹配）", Version: "v1",
			InputSchema: `{"type":"object","properties":{"path":{"type":"string"},"pattern":{"type":"string"}}}`,
			Risk:        tool.RiskRead, Approval: tool.ApprovalNever, Idempotent: true, TimeoutMs: 10000, MaxResultBytes: 32 * 1024,
			Handler: fileList,
		},
		{
			Name: "file_search", Description: "在工作区文本文件中全文检索子串（只读，返回文件/行号/摘录）", Version: "v1",
			InputSchema: `{"type":"object","properties":{"query":{"type":"string"},"path":{"type":"string"},"max_results":{"type":"integer"}},"required":["query"]}`,
			Risk:        tool.RiskRead, Approval: tool.ApprovalNever, Idempotent: true, TimeoutMs: 10000, MaxResultBytes: 32 * 1024,
			Handler: fileSearch,
		},
		{
			Name: "file_edit", Description: "对工作区内文件做精确唯一替换（受控写入，默认审批）", Version: "v1",
			InputSchema: `{"type":"object","properties":{"path":{"type":"string"},"old_text":{"type":"string"},"new_text":{"type":"string"}},"required":["path","old_text","new_text"]}`,
			Risk:        tool.RiskWrite, Approval: tool.ApprovalConditional, Idempotent: false, TimeoutMs: 15000, MaxResultBytes: 4 * 1024,
			Handler: fileEdit,
		},
		{
			Name: "workspace_write", Description: "在工作区内创建受控草稿文件（受控写入，默认审批）", Version: "v1",
			InputSchema:    `{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`,
			RequiredScopes: []string{"workspace:write"},
			Risk:           tool.RiskWrite, Approval: tool.ApprovalConditional, Idempotent: false, TimeoutMs: 15000, MaxResultBytes: 4 * 1024,
			Handler: workspaceWrite,
		},
		{
			Name: "http_fetch", Description: "抓取白名单域名的 HTTP GET 页面/接口（外部副作用，始终需审批）", Version: "v1",
			InputSchema: `{"type":"object","properties":{"url":{"type":"string"},"max_bytes":{"type":"integer"}},"required":["url"]}`,
			Risk:        tool.RiskExternal, Approval: tool.ApprovalAlways, Idempotent: false, TimeoutMs: 20000, MaxResultBytes: 64 * 1024,
			Handler: httpFetch,
		},
	}
	for _, d := range defs {
		if err := r.Register(d); err != nil {
			return err
		}
	}
	return nil
}

var (
	// workspaceRoot / knowledgeRoot 由 app 装配经 SetDirs 注入（配置化，
	// 缺省 ./workspace、./knowledge）；httpAllowlist 同理（空 = http_fetch 全拒）。
	workspaceRoot = "./workspace"
	knowledgeRoot = "./knowledge"
	httpAllowlist []string
)

// SetDirs 注入工作区与知识目录根路径。必须在 RegisterAll 之前调用。
func SetDirs(workspace, knowledge string) {
	workspaceRoot = workspace
	knowledgeRoot = knowledge
}

// SetHTTPAllowlist 注入 http_fetch 域名白名单（精确 host 匹配；空 = 禁用）。
func SetHTTPAllowlist(domains []string) {
	httpAllowlist = domains
}

func knowledgeSearch(_ context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Query string `json:"query"`
		TopK  int    `json:"top_k"`
	}
	if err := json.Unmarshal(args, &in); err != nil || strings.TrimSpace(in.Query) == "" {
		return "", fmt.Errorf("invalid query")
	}
	return fmt.Sprintf(`{"query":%q,"hits":[{"doc":"workspace handbook","snippet":"matched %q (stub, connect FTS later)"}]}`, in.Query, in.Query), nil
}

func cleanPath(p string) (string, error) {
	if p == "" || strings.Contains(p, "..") || filepath.IsAbs(p) {
		return "", fmt.Errorf("path not allowed")
	}
	abs := filepath.Join(workspaceRoot, filepath.Clean(p))
	rel, err := filepath.Rel(workspaceRoot, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path escapes workspace")
	}
	return abs, nil
}

func fileRead(_ context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Path     string `json:"path"`
		MaxBytes int    `json:"max_bytes"`
	}
	if err := json.Unmarshal(args, &in); err != nil || in.Path == "" {
		return "", fmt.Errorf("invalid path")
	}
	fp, err := cleanPath(in.Path)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(fp)
	if err != nil {
		return "", err
	}
	n := in.MaxBytes
	if n <= 0 || n > 64*1024 {
		n = 32 * 1024
	}
	if len(b) > n {
		b = b[:n]
	}
	out, _ := json.Marshal(map[string]string{"path": in.Path, "content": string(b)})
	return string(out), nil
}

func fileList(_ context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Path    string `json:"path"`
		Pattern string `json:"pattern"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("invalid input")
	}
	rel := in.Path
	if rel == "" {
		rel = "."
	}
	root, err := cleanPath(rel)
	if err != nil {
		return "", err
	}
	type entry struct {
		Name  string `json:"name"`
		IsDir bool   `json:"is_dir"`
		Size  int64  `json:"size"`
	}
	var entries []entry
	appendEntry := func(path string, d fs.DirEntry) {
		info, err := d.Info()
		if err != nil {
			return
		}
		relPath, err := filepath.Rel(root, path)
		if err != nil || relPath == "." {
			return
		}
		entries = append(entries, entry{Name: filepath.ToSlash(relPath), IsDir: d.IsDir(), Size: info.Size()})
	}
	if in.Pattern == "" {
		// 单层目录列举（path 缺省 = 工作区根）
		dirents, err := os.ReadDir(root)
		if err != nil {
			return "", err
		}
		for _, d := range dirents {
			appendEntry(filepath.Join(root, d.Name()), d)
		}
	} else {
		// 递归 + 按文件名 glob 匹配
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if d.Name() == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			ok, err := filepath.Match(in.Pattern, d.Name())
			if err != nil || !ok {
				return nil
			}
			appendEntry(path, d)
			return nil
		})
	}
	if entries == nil {
		entries = []entry{}
	}
	out, _ := json.Marshal(map[string]any{"entries": entries})
	return string(out), nil
}

func fileSearch(_ context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Query      string `json:"query"`
		Path       string `json:"path"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.Unmarshal(args, &in); err != nil || strings.TrimSpace(in.Query) == "" {
		return "", fmt.Errorf("invalid query")
	}
	root := workspaceRoot
	if in.Path != "" {
		sub, err := cleanPath(in.Path)
		if err != nil {
			return "", err
		}
		root = sub
	}
	hits := searchDir(root, in.Query, in.MaxResults)
	if hits == nil {
		hits = []searchHit{}
	}
	out, _ := json.Marshal(map[string]any{"hits": hits})
	return string(out), nil
}

func fileEdit(_ context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Path    string `json:"path"`
		OldText string `json:"old_text"`
		NewText string `json:"new_text"`
	}
	if err := json.Unmarshal(args, &in); err != nil || in.Path == "" {
		return "", fmt.Errorf("invalid input")
	}
	fp, err := cleanPath(in.Path)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(fp)
	if err != nil {
		return "", err
	}
	old := in.OldText
	if n := strings.Count(string(b), old); n != 1 {
		return "", fmt.Errorf("old_text must occur exactly once (got %d)", n)
	}
	updated := strings.Replace(string(b), old, in.NewText, 1)
	if err := os.WriteFile(fp, []byte(updated), 0o644); err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]string{"path": in.Path, "bytes_changed": fmt.Sprint(len(updated) - len(b))})
	return string(out), nil
}

func httpFetch(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		URL      string `json:"url"`
		MaxBytes int    `json:"max_bytes"`
	}
	if err := json.Unmarshal(args, &in); err != nil || in.URL == "" {
		return "", fmt.Errorf("invalid url")
	}
	u, err := url.Parse(in.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("only http/https URLs are allowed")
	}
	// 域名白名单精确匹配（先校验后请求）；空 = 禁用
	if len(httpAllowlist) == 0 {
		return "", fmt.Errorf("http_fetch disabled: allowlist empty")
	}
	host := u.Hostname()
	ok := false
	for _, d := range httpAllowlist {
		if strings.EqualFold(host, d) {
			ok = true
			break
		}
	}
	if !ok {
		return "", fmt.Errorf("host %q is not in the http_fetch allowlist", host)
	}
	n := in.MaxBytes
	if n <= 0 || n > 64*1024 {
		n = 64 * 1024
	}
	req, err := http.NewRequestWithContext(ctx, "GET", in.URL, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body := make([]byte, n)
	read, err := io.ReadFull(resp.Body, body)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{"status": resp.StatusCode, "body": string(body[:read])})
	return string(out), nil
}

func workspaceWrite(_ context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(args, &in); err != nil || in.Path == "" {
		return "", fmt.Errorf("invalid input")
	}
	fp, err := cleanPath(in.Path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(fp, []byte(in.Content), 0o644); err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]string{"path": in.Path, "bytes": fmt.Sprint(len(in.Content))})
	return string(out), nil
}
