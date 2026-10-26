package skill

import (
	"os"
	"path/filepath"
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
