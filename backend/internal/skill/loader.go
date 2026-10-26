package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type Meta struct {
	Name        string
	Description string
	Version     string
	WhenToUse   string
	Tools       []string
	Path        string
}

type Loader struct {
	dir    string
	mu     sync.RWMutex
	metas  []Meta
	bodies map[string]string
}

func NewLoader(dir string) *Loader {
	return &Loader{dir: dir, bodies: map[string]string{}}
}

func (l *Loader) Refresh() error {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var metas []Meta
	bodies := map[string]string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		fp := filepath.Join(l.dir, e.Name(), "SKILL.md")
		b, err := os.ReadFile(fp)
		if err != nil {
			continue
		}
		m := parseMeta(e.Name(), string(b))
		m.Path = fp
		metas = append(metas, m)
		bodies[m.Name] = string(b)
	}
	l.mu.Lock()
	l.metas, l.bodies = metas, bodies
	l.mu.Unlock()
	return nil
}

func parseMeta(name, body string) Meta {
	m := Meta{Name: name}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			break // frontmatter 区结束（首个空行），防止正文假阳性
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "#"))
		lower := strings.ToLower(line)
		switch {
		case strings.HasPrefix(lower, "description:"):
			m.Description = strings.TrimSpace(line[len("description:"):])
		case strings.HasPrefix(lower, "version:"):
			m.Version = strings.TrimSpace(line[len("version:"):])
		case strings.HasPrefix(lower, "when_to_use:"):
			m.WhenToUse = strings.TrimSpace(line[len("when_to_use:"):])
		case strings.HasPrefix(lower, "tools:"):
			m.Tools = splitCSV(line[len("tools:"):])
		}
	}
	return m
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (l *Loader) List() []Meta {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]Meta{}, l.metas...)
}

func (l *Loader) Get(name string) (string, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	b, ok := l.bodies[name]
	return b, ok
}

// ListReferences 返回 skill 的 references/ 目录下的 .md 文件名（排序）。
func (l *Loader) ListReferences(name string) ([]string, error) {
	dir, err := l.referencesDir(name)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("skill %q has no references: %w", name, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}

// ReadReference 读取 references/<ref>，ref 必须是目录内的普通文件名，内容上限 16KB。
func (l *Loader) ReadReference(name, ref string) (string, error) {
	dir, err := l.referencesDir(name)
	if err != nil {
		return "", err
	}
	if ref == "" || strings.ContainsAny(ref, `/\`) || filepath.IsAbs(ref) {
		return "", fmt.Errorf("invalid reference name %q", ref)
	}
	full := filepath.Clean(filepath.Join(dir, ref))
	if !strings.HasPrefix(full, dir+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid reference name %q", ref)
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return "", fmt.Errorf("read reference %q: %w", ref, err)
	}
	const refCap = 16 * 1024
	if len(b) > refCap {
		b = b[:refCap]
	}
	return string(b), nil
}

func (l *Loader) referencesDir(name string) (string, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	for _, m := range l.metas {
		if m.Name == name {
			return filepath.Join(filepath.Dir(m.Path), "references"), nil
		}
	}
	return "", fmt.Errorf("unknown or unavailable skill %q", name)
}
