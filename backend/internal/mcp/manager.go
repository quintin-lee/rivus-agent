package mcp

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// StatusEntry is one configured MCP server's live state (feeds GET /settings/tools).
type StatusEntry struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	Status  string   `json:"status"` // "up" | "error"
	Err     string   `json:"err,omitempty"`
	Tools   []Tool   `json:"tools"` // cached tools/list result
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
// (others continue). Idempotent for servers already connected. Per-server
// failures are reflected in Status(), not the return value; this always returns nil.
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
// the tool registry (Task 12 is that call site). Per-server failures are
// reflected in Status(), not the return value; this always returns nil.
func (m *Manager) Reconcile(ctx context.Context, specs []ServerSpec) error {
	keep := map[string]bool{}
	for _, s := range specs {
		keep[s.Name] = true
	}
	m.mu.Lock()
	var toStop []Transport
	for name, c := range m.clis {
		if !keep[name] {
			toStop = append(toStop, c.transport)
			delete(m.clis, name)
		}
	}
	// Drop stale status entries (e.g. connect failures) whose server is no
	// longer in the spec set and has no live client.
	for name := range m.status {
		if !keep[name] {
			delete(m.status, name)
		}
	}
	m.mu.Unlock()
	for _, tt := range toStop {
		_ = tt.Stop() // teardown outside the lock: Stop can block on child reap
	}
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
		m.setStatus(s.Name, s.Command, s.Args, "error", err.Error())
		return
	}
	c := NewClient(tt)
	ictx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := c.Initialize(ictx); err != nil {
		_ = tt.Stop() // best-effort teardown of the spawned child
		m.setStatus(s.Name, s.Command, s.Args, "error", "initialize: "+err.Error())
		return
	}
	tools, err := c.ListTools(ictx)
	if err != nil {
		_ = tt.Stop() // best-effort teardown of the spawned child
		m.setStatus(s.Name, s.Command, s.Args, "error", "tools/list: "+err.Error())
		return
	}
	m.mu.Lock()
	m.clis[s.Name] = c
	m.status[s.Name] = StatusEntry{Name: s.Name, Command: s.Command, Args: s.Args, Status: "up", Tools: tools}
	m.mu.Unlock()
}

// Call invokes tools/call on a server. A dead or missing connection returns a
// "disconnected" error; the wrapped client error keeps transport-read framing.
func (m *Manager) Call(ctx context.Context, server, tool string, args map[string]any) (string, error) {
	c, ok := m.clientFor(server)
	if !ok {
		return "", fmt.Errorf("mcp server %s disconnected", server)
	}
	out, err := c.Call(ctx, tool, args)
	if err != nil {
		return "", fmt.Errorf("mcp server %s: %w", server, err)
	}
	return out, nil
}

func (m *Manager) clientFor(name string) (*Client, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.clis[name]
	return c, ok
}

// Status returns a snapshot of every configured server's state. The returned
// entries share backing arrays with the manager (Tools slices and their InputSchema
// maps); callers must not mutate them in place.
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
func (m *Manager) setStatus(name, command string, args []string, status, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := StatusEntry{Name: name, Command: command, Args: args, Status: status}
	if errMsg != "" {
		e.Err = errMsg
	}
	m.status[name] = e
}

// StopAll stops every connection and clears all status. Per-server failures are
// reflected in Status(), not the return value; this always returns nil.
func (m *Manager) StopAll() error {
	m.mu.Lock()
	snapshot := m.clis
	m.clis = map[string]*Client{}
	m.status = map[string]StatusEntry{}
	m.mu.Unlock()
	for _, c := range snapshot {
		_ = c.transport.Stop() // teardown outside the lock: Stop can block on child reap
	}
	return nil
}
