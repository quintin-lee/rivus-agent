package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestManagerLifecycle(t *testing.T) {
	f := NewFakeTransportFactory()
	f.ByServer["fs"] = &FakeTransport{Tools: []string{"read"}, CallResult: "data"}
	m := NewManager(f)

	// SpawnAll: one up server
	if err := m.SpawnAll(context.Background(), []ServerSpec{{Name: "fs", Command: "npx"}}); err != nil {
		t.Fatal(err)
	}
	if st := m.Status(); len(st) != 1 || st[0].Status != "up" || len(st[0].Tools) != 1 {
		t.Fatalf("Status = %+v", st)
	}

	// Reconcile: remove fs, add git (crash mode)
	f.ByServer["git"] = &FakeTransport{Crash: true}
	if err := m.Reconcile(context.Background(), []ServerSpec{{Name: "git", Command: "git-mcp"}}); err != nil {
		t.Fatal(err)
	}
	st := m.Status()
	if len(st) != 1 || st[0].Name != "git" {
		t.Fatalf("after Reconcile Status = %+v", st)
	}
	if st[0].Status != "error" { // crashed server -> error, others continue
		t.Fatalf("git status = %q, want error", st[0].Status)
	}

	// Call on a crashed server -> "disconnected"
	if _, err := m.Call(context.Background(), "git", "status", nil); err == nil || !strings.Contains(err.Error(), "disconnected") {
		t.Fatalf("want disconnected error, got %v", err)
	}

	// StopAll clears live connections
	if err := m.StopAll(); err != nil {
		t.Fatal(err)
	}
	if len(m.Status()) != 0 {
		t.Fatalf("StopAll must clear status, got %+v", m.Status())
	}
}

// TestManagerMultiServerCoexist: one healthy + one crashed server can coexist;
// Status reports both (one "up", one "error").
func TestManagerMultiServerCoexist(t *testing.T) {
	f := NewFakeTransportFactory()
	f.ByServer["up"] = &FakeTransport{Tools: []string{"a"}, CallResult: "ok"}
	f.ByServer["dead"] = &FakeTransport{Crash: true}
	m := NewManager(f)
	if err := m.SpawnAll(context.Background(), []ServerSpec{
		{Name: "up", Command: "up-mcp"},
		{Name: "dead", Command: "dead-mcp"},
	}); err != nil {
		t.Fatal(err)
	}
	st := m.Status()
	if len(st) != 2 {
		t.Fatalf("Status len = %d, want 2: %+v", len(st), st)
	}
	var ups, errs int
	for _, e := range st {
		switch e.Status {
		case "up":
			ups++
		case "error":
			errs++
		default:
			t.Fatalf("unexpected status %q for %s", e.Status, e.Name)
		}
	}
	if ups != 1 || errs != 1 {
		t.Fatalf("want 1 up + 1 error, got %+v", st)
	}
}

// TestSpawnAllIdempotent: spawning the same already-connected server again is a
// no-op (no error, exactly one entry).
func TestSpawnAllIdempotent(t *testing.T) {
	f := NewFakeTransportFactory()
	f.ByServer["fs"] = &FakeTransport{Tools: []string{"read"}, CallResult: "ok"}
	m := NewManager(f)
	specs := []ServerSpec{{Name: "fs", Command: "npx"}}
	if err := m.SpawnAll(context.Background(), specs); err != nil {
		t.Fatal(err)
	}
	if err := m.SpawnAll(context.Background(), specs); err != nil {
		t.Fatalf("second SpawnAll must be a no-op, got %v", err)
	}
	st := m.Status()
	if len(st) != 1 || st[0].Status != "up" {
		t.Fatalf("after double SpawnAll Status = %+v", st)
	}
}

// TestReconcileKeepAndDrop: reconciling to a smaller set keeps the retained
// client untouched and removes the dropped server from Status.
func TestReconcileKeepAndDrop(t *testing.T) {
	f := NewFakeTransportFactory()
	f.ByServer["a"] = &FakeTransport{Tools: []string{"read"}, CallResult: "ok"}
	f.ByServer["b"] = &FakeTransport{Tools: []string{"write"}, CallResult: "ok"}
	m := NewManager(f)
	if err := m.SpawnAll(context.Background(), []ServerSpec{
		{Name: "a", Command: "a-mcp"},
		{Name: "b", Command: "b-mcp"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.Reconcile(context.Background(), []ServerSpec{{Name: "a", Command: "a-mcp"}}); err != nil {
		t.Fatal(err)
	}
	st := m.Status()
	if len(st) != 1 || st[0].Name != "a" || st[0].Status != "up" {
		t.Fatalf("after Reconcile Status = %+v, want only a up", st)
	}
	if out, err := m.Call(context.Background(), "a", "read", nil); err != nil || out != "ok" {
		t.Fatalf("kept server still serviceable: out=%q err=%v", out, err)
	}
}

// TestReconcileStaleErrorCleanup: a server whose connect failed (error status,
// no live client) is dropped from Status when reconciled away from the spec set.
func TestReconcileStaleErrorCleanup(t *testing.T) {
	f := NewFakeTransportFactory()
	f.ByServer["dead"] = &FakeTransport{Crash: true}
	f.ByServer["ok"] = &FakeTransport{Tools: []string{"read"}, CallResult: "ok"}
	m := NewManager(f)
	if err := m.SpawnAll(context.Background(), []ServerSpec{
		{Name: "dead", Command: "dead-mcp"},
		{Name: "ok", Command: "ok-mcp"},
	}); err != nil {
		t.Fatal(err)
	}
	if st := m.Status(); len(st) != 2 {
		t.Fatalf("pre-reconcile Status = %+v, want 2", st)
	}
	if err := m.Reconcile(context.Background(), []ServerSpec{{Name: "ok", Command: "ok-mcp"}}); err != nil {
		t.Fatal(err)
	}
	st := m.Status()
	if len(st) != 1 || st[0].Name != "ok" || st[0].Status != "up" {
		t.Fatalf("stale error entry not cleaned: %+v", st)
	}
}

// ---- 批3: reconciler + call timeout ----

// flakyFactory: 前 fails 次 New 返回错误，之后委托 inner。
type flakyFactory struct {
	mu    sync.Mutex
	fails int
	inner TransportFactory
}

func (f *flakyFactory) New(ctx context.Context, spec ServerSpec) (Transport, error) {
	f.mu.Lock()
	if f.fails > 0 {
		f.fails--
		f.mu.Unlock()
		return nil, fmt.Errorf("flaky: boom")
	}
	f.mu.Unlock()
	return f.inner.New(ctx, spec)
}

func TestStartReconcilerRetriesFailedServer(t *testing.T) {
	m := NewManager(&flakyFactory{fails: 1, inner: NewFakeTransportFactory()})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	specs := []ServerSpec{{Name: "flaky", Command: "echo"}}
	_ = m.SpawnAll(ctx, specs)
	if st := m.Status()[0]; st.Status != "error" {
		t.Fatalf("initial status = %q, want error", st.Status)
	}
	m.StartReconciler(ctx, 10*time.Millisecond)
	deadline := time.Now().Add(2 * time.Second)
	for {
		st := m.Status()
		if len(st) == 1 && st[0].Status == "up" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server not recovered within 2s: %+v", st)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestStartReconcilerRemovedSpecsStayRemoved(t *testing.T) {
	f := NewFakeTransportFactory()
	m := NewManager(f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = m.SpawnAll(ctx, []ServerSpec{
		{Name: "a", Command: "x"}, {Name: "b", Command: "y"},
	})
	// 同步 Reconcile 只保留 a：b 被停止；spec 记忆必须同步更新为 [a]，
	// 否则 reconciler tick 会把 b 复活。
	_ = m.Reconcile(ctx, []ServerSpec{{Name: "a", Command: "x"}})
	m.StartReconciler(ctx, 10*time.Millisecond)
	time.Sleep(60 * time.Millisecond) // 数个 tick
	for _, e := range m.Status() {
		if e.Name == "b" {
			t.Fatalf("removed server b resurrected: %+v", e)
		}
	}
	for _, e := range m.Status() {
		if e.Name == "a" && e.Status != "up" {
			t.Fatalf("server a degraded: %+v", e)
		}
	}
}

type oneShotFactory struct{ tt Transport }

func (f *oneShotFactory) New(context.Context, ServerSpec) (Transport, error) { return f.tt, nil }

// callBlockingTransport: 让 initialize/tools/list 正常走 FakeTransport，
// 从第 3 个请求（tools/call）起在 WriteLine 上阻塞，直到 release 关闭。
type callBlockingTransport struct {
	*FakeTransport
	release chan struct{}
	mu      sync.Mutex
	calls   int
}

func (b *callBlockingTransport) WriteLine(p []byte) error {
	b.mu.Lock()
	b.calls++
	n := b.calls
	b.mu.Unlock()
	if n >= 3 { // 第 3 个请求 = tools/call：阻塞直到 release 关闭
		<-b.release
	}
	return b.FakeTransport.WriteLine(p)
}

func (b *callBlockingTransport) Stop() error { return nil }

func TestCallTimeout(t *testing.T) {
	release := make(chan struct{})
	m := NewManager(&oneShotFactory{tt: &callBlockingTransport{
		FakeTransport: &FakeTransport{Tools: []string{"echo"}, CallResult: "ok"},
		release:       release,
	}})
	m.callTimeout = 50 * time.Millisecond // 注入短超时（默认 30s 不适合测试）
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = m.SpawnAll(ctx, []ServerSpec{{Name: "slow", Command: "x"}})
	start := time.Now()
	_, err := m.Call(ctx, "slow", "echo", nil)
	elapsed := time.Since(start)
	if err == nil || !strings.Contains(err.Error(), "mcp call timeout") {
		t.Fatalf("err = %v, want mcp call timeout", err)
	}
	if elapsed > time.Second {
		t.Fatalf("Call blocked %s, want <=1s", elapsed)
	}
	close(release) // 释放阻塞的 WriteLine，让底层 goroutine 退出
	time.Sleep(20 * time.Millisecond)
}

// pushTransport：完整 JSON-RPC 假服务器；响应前先吐出 inject 队列的通知行，
// tools/list 依次返回 tools 队列中的工具集（耗尽后重复最后一个）。
type pushTransport struct {
	mu      sync.Mutex
	toolsQ  [][]string
	inject  [][]byte
	pending *jsonRequest
	toolIdx int
}

var _ Transport = (*pushTransport)(nil)

func newPushTransport(toolsSets [][]string) *pushTransport {
	return &pushTransport{toolsQ: toolsSets}
}

func (t *pushTransport) enqueueNotification(line string) {
	t.mu.Lock()
	t.inject = append(t.inject, []byte(line))
	t.mu.Unlock()
}

func (t *pushTransport) WriteLine(p []byte) error {
	var req jsonRequest
	if err := json.Unmarshal(p, &req); err != nil {
		return err
	}
	t.mu.Lock()
	t.pending = &req
	t.mu.Unlock()
	return nil
}

func (t *pushTransport) ReadLine() ([]byte, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.inject) > 0 {
		line := t.inject[0]
		t.inject = t.inject[1:]
		return line, nil
	}
	req := t.pending
	if req == nil {
		return nil, fmt.Errorf("pushTransport: read before write")
	}
	t.pending = nil
	switch req.Method {
	case "initialize":
		return []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2024-11-05","capabilities":{}}}`, req.ID)), nil
	case "tools/list":
		set := t.toolsQ[t.toolIdx]
		if t.toolIdx < len(t.toolsQ)-1 {
			t.toolIdx++
		}
		tools := make([]string, 0, len(set))
		for _, n := range set {
			tools = append(tools, fmt.Sprintf(`{"name":%q,"description":"push tool"}`, n))
		}
		return []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"tools":[%s]}}`, req.ID, strings.Join(tools, ","))), nil
	case "tools/call":
		return []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"content":[{"type":"text","text":"ok"}]}}`, req.ID)), nil
	default:
		return nil, fmt.Errorf("pushTransport: unknown method %q", req.Method)
	}
}

func (t *pushTransport) Stop() error { return nil }

func TestToolsListChangedNotificationRefreshesStatus(t *testing.T) {
	pt := newPushTransport([][]string{{"read"}, {"read", "write"}})
	m := NewManager(&oneShotFactory{tt: pt})
	if err := m.SpawnAll(context.Background(), []ServerSpec{{Name: "fs", Command: "npx"}}); err != nil {
		t.Fatal(err)
	}
	if st := m.Status(); len(st) != 1 || len(st[0].Tools) != 1 {
		t.Fatalf("initial Status = %+v", st)
	}
	pt.enqueueNotification(`{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}`)
	if _, err := m.Call(context.Background(), "fs", "ping", nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		st := m.Status()
		if len(st) == 1 && len(st[0].Tools) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("tools not refreshed: %+v", st)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
