package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// StatusEntry is one configured MCP server's live state (feeds GET /settings/tools).
type StatusEntry struct {
	Name    string   `json:"name"`
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	Type    string   `json:"type,omitempty"` // "" | "stdio" | "http"
	URL     string   `json:"url,omitempty"`
	Status  string   `json:"status"` // "up" | "error"
	Err     string   `json:"err,omitempty"`
	Tools   []Tool   `json:"tools"` // cached tools/list result
}

// Manager owns all live MCP server connections.
type Manager struct {
	mu          sync.RWMutex
	factory     TransportFactory
	clis        map[string]*Client // keyed by server name
	status      map[string]StatusEntry
	specs       []ServerSpec  // 最近一次 SpawnAll/Reconcile 的期望服务器集（供周期重连）
	callTimeout time.Duration // 单次 tools/call 上限；NewManager 设 30s，测试可注入
}

// NewManager builds a Manager over a TransportFactory (stdio in prod, fake in tests).
func NewManager(factory TransportFactory) *Manager {
	return &Manager{factory: factory, clis: map[string]*Client{}, status: map[string]StatusEntry{}, callTimeout: 30 * time.Second}
}

// SpawnAll connects to every server in specs; a failed initialize marks it "error"
// (others continue). Idempotent for servers already connected. Per-server
// failures are reflected in Status(), not the return value; this always returns nil.
func (m *Manager) SpawnAll(ctx context.Context, specs []ServerSpec) error {
	m.rememberSpecs(specs)
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
	m.rememberSpecs(specs)
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
		m.setStatus(s, "error", err.Error())
		return
	}
	c := NewClient(tt)
	ictx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := c.Initialize(ictx); err != nil {
		_ = tt.Stop() // best-effort teardown of the spawned child
		m.setStatus(s, "error", "initialize: "+err.Error())
		return
	}
	tools, err := c.ListTools(ictx)
	if err != nil {
		_ = tt.Stop() // best-effort teardown of the spawned child
		m.setStatus(s, "error", "tools/list: "+err.Error())
		return
	}
	// 服务器推送 notifications/tools/list_changed 时立即重列工具（比 30s Reconciler 更快）。
	c.SetNotificationHandler(func(method string, params json.RawMessage) {
		if method != "notifications/tools/list_changed" {
			return
		}
		go m.relistTools(context.Background(), s)
	})
	m.mu.Lock()
	m.clis[s.Name] = c
	m.status[s.Name] = StatusEntry{Name: s.Name, Command: s.Command, Args: s.Args, Type: s.Type, URL: s.URL, Status: "up", Tools: tools}
	m.mu.Unlock()
}

// Call invokes tools/call on a server with a per-call timeout (default 30s).
// The underlying client read is not context-aware (blocking ReadLine), so the
// timeout guarantees only that THE CALLER returns promptly; a wedged stream
// keeps the per-client mutex until the transport unblocks.
func (m *Manager) Call(ctx context.Context, server, tool string, args map[string]any) (string, error) {
	c, ok := m.clientFor(server)
	if !ok {
		return "", fmt.Errorf("mcp server %s disconnected", server)
	}
	cctx, cancel := context.WithTimeout(ctx, m.callTimeout)
	defer cancel()
	type callResult struct {
		out string
		err error
	}
	ch := make(chan callResult, 1)
	go func() {
		out, err := c.Call(cctx, tool, args)
		ch <- callResult{out, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return "", fmt.Errorf("mcp server %s: %w", server, r.err)
		}
		return r.out, nil
	case <-cctx.Done():
		return "", fmt.Errorf("mcp call timeout after %s", m.callTimeout)
	}
}

func (m *Manager) clientFor(name string) (*Client, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.clis[name]
	return c, ok
}

// StartReconciler launches a goroutine that re-Reconciles the last known spec
// set every interval (<=0 defaults to 30s). Servers that are up are skipped;
// error/missing ones are retried; servers removed from the remembered specs
// stay stopped. Exits when ctx is cancelled.
func (m *Manager) StartReconciler(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				specs := m.snapshotSpecs()
				if len(specs) == 0 {
					continue
				}
				_ = m.Reconcile(ctx, specs)
			}
		}
	}()
}

// rememberSpecs stores the latest desired server set (SpawnAll/Reconcile).
func (m *Manager) rememberSpecs(specs []ServerSpec) {
	cp := make([]ServerSpec, len(specs))
	copy(cp, specs)
	m.mu.Lock()
	m.specs = cp
	m.mu.Unlock()
}

func (m *Manager) snapshotSpecs() []ServerSpec {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ServerSpec, len(m.specs))
	copy(out, m.specs)
	return out
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
func (m *Manager) setStatus(s ServerSpec, status, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := StatusEntry{Name: s.Name, Command: s.Command, Args: s.Args, Type: s.Type, URL: s.URL, Status: status}
	if errMsg != "" {
		e.Err = errMsg
	}
	m.status[s.Name] = e
}

// relistTools 在收到 tools/list_changed 通知后重列工具并刷新状态。
// 与进行中的 rpc 共享 client 锁（排队等待）；工具集未变化时跳过写入（通知风暴防护）。
func (m *Manager) relistTools(ctx context.Context, s ServerSpec) {
	c, ok := m.clientFor(s.Name)
	if !ok {
		return
	}
	tools, err := c.ListTools(ctx)
	if err != nil {
		m.setStatus(s, "error", "tools/list: "+err.Error())
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if entry, ok := m.status[s.Name]; ok && sameTools(entry.Tools, tools) {
		return
	}
	m.status[s.Name] = StatusEntry{Name: s.Name, Command: s.Command, Args: s.Args, Type: s.Type, URL: s.URL, Status: "up", Tools: tools}
}

func sameTools(a, b []Tool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Description != b[i].Description {
			return false
		}
	}
	return true
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
