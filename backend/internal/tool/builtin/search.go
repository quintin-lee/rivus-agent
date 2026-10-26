package builtin

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	searchMaxFileBytes = 512 * 1024 // 单文件上限，超过跳过
	searchSnippetBytes = 200        // 摘录截断
)

type searchHit struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Snippet string `json:"snippet"`
}

// searchDir 在 root 下递归做逐行子串检索（纯 Go，无 FTS 依赖）。
// 跳过：.git 目录、>512KB 文件、疑似二进制（首 8KB 含 NUL）。maxResults<=0 → 20。
// root 不存在时返回空切片（容错，不报错）。
func searchDir(root, query string, maxResults int) []searchHit {
	if strings.TrimSpace(query) == "" || maxResults <= 0 {
		return nil
	}
	if maxResults > 100 {
		maxResults = 100
	}
	var hits []searchHit
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || hits != nil && len(hits) >= maxResults {
			return fs.SkipAll
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > searchMaxFileBytes {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if bytes.IndexByte(b[:min(len(b), 8192)], 0) >= 0 {
			return nil // 疑似二进制
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		for i, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, query) {
				sn := strings.TrimSpace(line)
				if len(sn) > searchSnippetBytes {
					sn = sn[:searchSnippetBytes]
				}
				hits = append(hits, searchHit{Path: rel, Line: i + 1, Snippet: sn})
				if len(hits) >= maxResults {
					return fs.SkipAll
				}
			}
		}
		return nil
	})
	return hits
}
