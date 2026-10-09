package builtin

import (
	"context"
	"encoding/json"
	"fmt"
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
			Name: "workspace_write", Description: "在工作区内创建受控草稿文件（受控写入，默认审批）", Version: "v1",
			InputSchema:    `{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`,
			RequiredScopes: []string{"workspace:write"},
			Risk:           tool.RiskWrite, Approval: tool.ApprovalConditional, Idempotent: false, TimeoutMs: 15000, MaxResultBytes: 4 * 1024,
			Handler: workspaceWrite,
		},
	}
	for _, d := range defs {
		if err := r.Register(d); err != nil {
			return err
		}
	}
	return nil
}

var workspaceRoot = "./workspace"

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
