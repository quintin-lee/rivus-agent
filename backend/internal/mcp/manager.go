package mcp

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// StatusEntry is one configured MCP server's live state (feeds GET /settings/tools).
type StatusEntry struct {
	Name    string `json:"name"`
	Command string `json:"command"`
	Status  string `json:"status"` // "up" | "down" | "error"
	Err     string `json:"err,omitempty"`
	Tools   []Tool `json:"tools"` // cached tools/list result
}

// Manager owns all live MCP server connections.
type Manager struct {
	mu      sync.RWMutex
	factory TransportFactory
	clis    map[string]*Client // keyed by server name
	status  map[string]StatusEntry
}

// NewManager builds a Manager over a TransportFactory (stdio in prod, fake in tests).
func NewManager(factory TransportFactory) *Manager {
	return &Manager{factory: factory, clis: map[string]*Client{}, status: map[string]StatusEntry{}}
}

// SpawnAll connects to every server in specs; a failed initialize marks it "error"
// (logged, others continue). Idempotent for servers already connected.
func (m *Manager) SpawnAll(ctx context.Context, specs []ServerSpec) error {
	for _, s := range specs {
		if _, ok := m.clientFor(s.Name); ok {
			continue
		}
		m.connect(ctx, s)
	}
	return nil
}

// Reconcile stops servers not in specs and spawns new ones. It does NOT mutate
// the tool registry (Task 12 is that call site).
func (m *Manager) Reconcile(ctx context.Context, specs []ServerSpec) error {
	keep := map[string]bool{}
	for _, s := range specs {
		keep[s.Name] = true
	}
	m.mu.Lock()
	for name := range m.clis {
		if !keep[name] {
			_ = m.clis[name].transport.Stop()
			delete(m.clis, name)
			delete(m.status, name)
		}
	}
	m.mu.Unlock()
	for _, s := range specs {
		if _, ok := m.clientFor(s.Name); !ok {
			m.connect(ctx, s)
		}
	}
	return nil
}

func (m *Manager) connect(ctx context.Context, s ServerSpec) {
	tt, err := m.factory.New(ctx, s)
	if err != nil {
		m.setStatus(s.Name, s.Command, "error", err.Error())
		return
	}
	c := NewClient(tt)
	ictx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := c.Initialize(ictx); err != nil {
		m.setStatus(s.Name, s.Command, "error", "initialize: "+err.Error())
		return
	}
	tools, err := c.ListTools(ictx)
	if err != nil {
		m.setStatus(s.Name, s.Command, "error", "tools/list: "+err.Error())
		return
	}
	m.mu.Lock()
	m.clis[s.Name] = c
	m.status[s.Name] = StatusEntry{Name: s.Name, Command: s.Command, Status: "up", Tools: tools}
	m.mu.Unlock()
}

// Call invokes tools/call on a server. A dead connection returns a "disconnected" error.
func (m *Manager) Call(ctx context.Context, server, tool string, args map[string]any) (string, error) {
	c, ok := m.clientFor(server)
	if !ok {
		return "", fmt.Errorf("mcp server %s disconnected", server)
	}
	out, err := c.Call(ctx, tool, args)
	if err != nil {
		return "", fmt.Errorf("mcp server %s disconnected: %w", server, err)
	}
	return out, nil
}

func (m *Manager) clientFor(name string) (*Client, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.clis[name]
	return c, ok
}

// Status returns a snapshot of every configured server's state.
func (m *Manager) Status() []StatusEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]StatusEntry, 0, len(m.status))
	for _, e := range m.status {
		out = append(out, e)
	}
	return out
}

// setStatus records one server's live state; used for error paths in connect.
func (m *Manager) setStatus(name, command, status, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := StatusEntry{Name: name, Command: command, Status: status}
	if errMsg != "" {
		e.Err = errMsg
	}
	m.status[name] = e
}

// StopAll stops every connection and clears all status.
func (m *Manager) StopAll() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.clis {
		_ = c.transport.Stop()
	}
	m.clis = map[string]*Client{}
	m.status = map[string]StatusEntry{}
	return nil
}
