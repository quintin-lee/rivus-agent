# Round-8 批3 MCP 深化 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** MCP 服务器故障后自动重连（30s 周期 Reconcile）+ 工具调用 30s 超时兜底。

**Architecture:** Manager 增加specs 记忆（SpawnAll/Reconcile 时记住最新期望列表）与 StartReconciler 常驻 goroutine（fixed ticker 调 Reconcile(snapshot)，up 的跳过、error 的重试、已移除的不再复活）。Call 包一层 `context.WithTimeout` + goroutine/select 竞速——底层 `Client.rpc` 的 `ReadLine` 无 ctx 感知（阻塞读），超时只能保证**调用方**拿到错误；被卡住的底层调用会持有 client 互斥锁直到 transport 返回，这是现有架构约束，plan 不改 Transport 接口（YAGNI，记入 spec 偏差）。

**Tech Stack:** Go stdlib（time.Ticker、context）、现有 mcp 包 + FakeTransportFactory 测试设施。零新依赖。

**Spec:** docs/superpowers/specs/2026-10-27-structure-frontend-mcp-design.md §批3

---

## Chunk 1

### Task 1: Manager specs 记忆 + StartReconciler（TDD）

**Files:**
- Modify: `backend/internal/mcp/manager.go`
- Test: `backend/internal/mcp/manager_test.go`（追加）
- Modify: `backend/internal/app/app.go`（接线）

- [ ] **Step 1: 写失败测试（追加到 manager_test.go）**

```go
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
```

注：现有测试文件 import 已含 context/fmt/sync/time 中的大部分，缺则补。`m.Status()` 返回 map 遍历无序——上面用 len+名称匹配，与现有测试风格一致（现有 TestReconcileKeepAndDrop 用辅助查找，可直接复用其 helper 若存在；没有则内联循环）。

- [ ] **Step 2: 运行确认失败**

Run: `cd backend && go test ./internal/mcp/ -run 'TestStartReconciler' -count=1`
Expected: FAIL — `m.StartReconciler undefined`（编译错误，RED）

- [ ] **Step 3: 最小实现（manager.go）**

Manager struct 增加 `specs []ServerSpec`；新增三个方法；SpawnAll/Reconcile 开头各加一行记忆：

```go
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
```

`SpawnAll` 第一行加 `m.rememberSpecs(specs)`；`Reconcile` 第一行（keep 构造前）加 `m.rememberSpecs(specs)`。

- [ ] **Step 4: 运行确认通过**

Run: `cd backend && go test ./internal/mcp/ -count=1`
Expected: PASS（新旧全部）

- [ ] **Step 5: app.go 接线**

`app.go` Start() 中 `if tc.MCPEnabled { ... }` 块内、`registry.Replace` 之后加：

```go
		a.manager.StartReconciler(ctx, 30*time.Second) // 故障服务器每 30s 自动重连
```

（`ctx` 为 Start 的应用级 ctx，Stop 时随 Shutdown 生命周期取消；`time` 已 import。）

Run: `cd backend && go build ./... && go vet ./... && go test ./internal/mcp/ ./internal/app/... -count=1`
Expected: 全绿

- [ ] **Step 6: Commit**

```bash
git add backend/internal/mcp/manager.go backend/internal/mcp/manager_test.go backend/internal/app/app.go
git commit -m "feat(mcp): periodic reconciler retries failed servers every 30s"
```

### Task 2: Call 30s 超时（TDD）

**Files:**
- Modify: `backend/internal/mcp/manager.go`
- Test: `backend/internal/mcp/manager_test.go`（追加）

- [ ] **Step 1: 写失败测试（追加）**

```go
// blockingTransport: WriteLine 成功，ReadLine 永久阻塞直到 release 关闭。
type blockingTransport struct{ release chan struct{} }

func (b *blockingTransport) WriteLine(_ []byte) error { return nil }
func (b *blockingTransport) ReadLine() ([]byte, error) {
	<-b.release
	return nil, io.EOF
}
func (b *blockingTransport) Close() error { return nil }

type oneShotFactory struct{ tt Transport }

func (f *oneShotFactory) New(context.Context, ServerSpec) (Transport, error) { return f.tt, nil }

func TestCallTimeout(t *testing.T) {
	release := make(chan struct{})
	m := NewManager(&oneShotFactory{tt: &blockingTransport{release: release}})
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
	close(release) // 释放阻塞的 ReadLine，让底层 goroutine 退出
	time.Sleep(20 * time.Millisecond)
}
```

注：import 若缺需补 `io`、`strings`。

- [ ] **Step 2: 运行确认失败**

Run: `cd backend && go test ./internal/mcp/ -run TestCallTimeout -count=1`
Expected: FAIL — 挂起直到测试超时或 `m.callTimeout undefined`（RED）

- [ ] **Step 3: 最小实现**

Manager struct 增加 `callTimeout time.Duration`；`NewManager` 设 `callTimeout: 30 * time.Second`（spec 的 30s 常量以字段默认值形式存在，测试可注入；记入 spec 偏差）。`Call` 改为：

```go
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
```

- [ ] **Step 4: 运行确认通过**

Run: `cd backend && go test ./internal/mcp/ -count=1 && gofmt -l internal/mcp/`
Expected: PASS + 无 gofmt 输出

- [ ] **Step 5: Commit**

```bash
git add backend/internal/mcp/manager.go backend/internal/mcp/manager_test.go
git commit -m "feat(mcp): 30s per-call timeout for tool invocations"
```

### Task 3: 文档 + 批3 验收

**Files:**
- Modify: `backend/README.md`、`backend/README.en.md`（MCP 段）
- Modify: `docs/superpowers/specs/2026-10-27-structure-frontend-mcp-design.md`（§批3 偏差回写）

- [ ] **Step 1: spec §批3 追加执行偏差段**

blockquote 追加两点：(1) Call 超时实现为 goroutine+select 竞速——Client.rpc 的 ReadLine 无 ctx 感知，超时只保证调用方返回，底层调用继续持有 client 锁（架构现状，不改 Transport 接口）；(2) 30s 以 `callTimeout` 字段默认值落地（NewManager 设 30s），测试可注入短超时。

- [ ] **Step 2: README zh/en MCP 段各加一句**

zh（backend/README.md MCP 段）：`故障服务器由后台 reconciler 每 30s 自动重连；每次工具调用有 30s 超时保护。`
en：`Failed servers are re-connected automatically by a background reconciler every 30s; each tool call is guarded by a 30s timeout.`

- [ ] **Step 3: 全量验收**

```bash
cd backend && go build ./... && go vet ./... && go test ./... -count=1 -timeout 600s && make test-eval
cd frontend && npx tsc --noEmit && npx vitest run
git status --short   # 仅 .omo/ 未跟踪
```

Expected: 后端全绿（mcp 新增 3 测试）、evals 15 场景、前端 23 files/144 tests、树干净

- [ ] **Step 4: Commit**

```bash
git add backend/README.md backend/README.en.md docs/superpowers/specs/2026-10-27-structure-frontend-mcp-design.md
git commit -m "docs(mcp): reconciler retry and call timeout notes (zh/en) + spec deviations"
```

---

## Self-review notes

1. **测试无泄漏**：TestCallTimeout close(release) + sleep 让 ReadLine 返回 io.EOF → rpc 返回 disconnected → goroutine 退出释放 c.mu。
2. **Ticker goroutine 生命周期**：全部测试用 `defer cancel()` 的 ctx，goroutine 退出有保证。
3. **Status() map 无序**：断言用名称匹配，不复用顺序假设。
4. **rememberSpecs 拷贝**：入参与快照都深拷贝 slice（ServerSpec 值类型，Args 共享底层数组可接受——specs 来源为配置解码，无人原地改 Args）。
5. **app.go Stop 顺序**：Stop 先 StopAll 后 Shutdown；reconciler 依赖 ctx 取消退出——Start 的 ctx 在 Start 返回（ListenAndServe 出错）后不再被取消时，Stop(ctx) 的调用方负责取消该 ctx（现有 app 生命周期已如此，无需改动）。
