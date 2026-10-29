# Round 19 实施计划：文档补遗 + MCP 通知推送 + evals 32

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修补 R17/R18 文档漂移（sessions.md q 参数、CHANGELOG），实现 MCP `notifications/tools/list_changed` 推送（client 通知跳过 + manager 立即重列工具），evals 30→32 覆盖 rename/search。

**Architecture:** client.rpc 读循环跳过通知行并在释放锁后分发；manager 注册 handler 起 goroutine relistTools（受 c.mu 串行化）。evals 复用 run-based 框架，会话 API 断言放 PostCheck。

**Tech Stack:** Go 1.27；无新依赖。

**Spec:** docs/superpowers/specs/2026-10-29-round19-docs-mcp-notify-evals-design.md（9fdfe96）

---

## Chunk 1：批 1 文档补遗（纯文档）

### Task 1: sessions.md q 参数 + CHANGELOG

**Files:**
- Modify: `docs/api/sessions.md`（GET /api/v1/sessions 小节）
- Modify: `docs/en/api/sessions.md`（同）
- Modify: `CHANGELOG.md`（[Unreleased] Added）

- [ ] **Step 1: sessions.md zh**——`## GET /api/v1/sessions` 小节开头段落改为：

```markdown
当前 owner 的会话列表。

**Query 参数**：`q`（可选）——按标题大小写不敏感子串匹配（`LOWER(title) LIKE`）；省略返回全部。
```

curl 示例改为两条：

```bash
curl localhost:8080/api/v1/sessions
curl "localhost:8080/api/v1/sessions?q=重构"
```

- [ ] **Step 2: sessions.md en**——镜像（`**Query params**: `q` (optional) — case-insensitive substring match on the title; omit to list all.`）。

- [ ] **Step 3: CHANGELOG.md**——`### Added` 列表尾部（批内合适位置）追加：

```markdown
- **会话重命名**：`PATCH /api/v1/sessions/{id}`（属主校验，空标题 400）+ 前端行内改名（铅笔入口，Enter/blur 提交、Esc 取消）。
- **会话搜索**：`GET /api/v1/sessions?q=`（标题大小写不敏感子串过滤）+ 前端 200ms 防抖搜索框。
```

- [ ] **Step 4: 验证 + Commit**

```bash
grep -c "q" docs/api/sessions.md docs/en/api/sessions.md   # 各 ≥3
grep -c "会话重命名\|会话搜索" CHANGELOG.md               # ≥2
git add docs/api/sessions.md docs/en/api/sessions.md CHANGELOG.md
git commit -m "docs: sessions list q param (zh/en) + CHANGELOG entries for rename and search"
```

---

## Chunk 2：批 2 MCP 通知推送（TDD）

### Task 2: client 通知跳过 + 分发（TDD）

**Files:**
- Modify: `backend/internal/mcp/client.go`
- Test: `backend/internal/mcp/client_test.go`（追加）

- [ ] **Step 1: 写失败测试**（client_test.go 追加，package mcp）：

```go
// notifyScriptTransport 按预排队列吐行：先通知、后响应。
type notifyScriptTransport struct {
	mu    sync.Mutex
	lines [][]byte
	sent  []jsonRequest
}

func (t *notifyScriptTransport) WriteLine(p []byte) error {
	var req jsonRequest
	if err := json.Unmarshal(p, &req); err != nil {
		return err
	}
	t.mu.Lock()
	t.sent = append(t.sent, req)
	t.mu.Unlock()
	return nil
}

func (t *notifyScriptTransport) ReadLine() ([]byte, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.lines) == 0 {
		return nil, io.EOF
	}
	line := t.lines[0]
	t.lines = t.lines[1:]
	return line, nil
}
func (t *notifyScriptTransport) Stop() {}

func TestRPCSkipsNotificationsAndDispatches(t *testing.T) {
	tr := &notifyScriptTransport{lines: [][]byte{
		[]byte(`{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}`),
		[]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"read"}]}}`),
	}}
	c := NewClient(tr)
	var gotMethod string
	var gotParams json.RawMessage
	c.SetNotificationHandler(func(m string, p json.RawMessage) {
		gotMethod, gotParams = m, p
	})
	tools, err := c.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "read" {
		t.Fatalf("tools = %+v", tools)
	}
	if gotMethod != "notifications/tools/list_changed" {
		t.Fatalf("notify method = %q", gotMethod)
	}
	if len(gotParams) != 0 {
		t.Fatalf("params = %s", gotParams)
	}

	// 无 handler 不 panic
	tr2 := &notifyScriptTransport{lines: [][]byte{
		[]byte(`{"jsonrpc":"2.0","method":"notifications/other"}`),
		[]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`),
	}}
	c2 := NewClient(tr2)
	c2.SetNotificationHandler(nil)
	if _, err := c2.ListTools(context.Background()); err != nil {
		t.Fatal(err)
	}
}
```

（import 按文件现状补 `io`/`sync`/`encoding/json`/`context`。）

- [ ] **Step 2: 跑测试确认失败**

```bash
cd backend && go test ./internal/mcp/ -run TestRPCSkipsNotifications -count=1
```
期望：`SetNotificationHandler undefined`（或 id-mismatch 错误）。

- [ ] **Step 3: 实现 client.go**

结构体加字段与 setter：

```go
type Client struct {
	transport Transport
	id        int32
	onNotify  func(method string, params json.RawMessage)

	mu sync.Mutex
}

// SetNotificationHandler 注册服务器通知回调（可为 nil）；回调在 rpc 释放锁后调用。
func (c *Client) SetNotificationHandler(fn func(method string, params json.RawMessage)) {
	c.onNotify = fn
}
```

rpc 拆两层：外层分发通知，内层持锁循环：

```go
type mcpNotification struct {
	method string
	params json.RawMessage
}

func (c *Client) rpc(ctx context.Context, method string, params any) (json.RawMessage, error) {
	result, notes, err := c.rpcLocked(ctx, method, params)
	for _, n := range notes {
		if c.onNotify != nil {
			c.onNotify(n.method, n.params)
		}
	}
	return result, err
}

func (c *Client) rpcLocked(ctx context.Context, method string, params any) (json.RawMessage, []mcpNotification, error) {
	// 原 rpc 主体；读循环改为：
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.transport.WriteLine(req); err != nil { ... }
	var notes []mcpNotification
	for {
		line, readErr := c.transport.ReadLine()
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, nil, fmt.Errorf("mcp server disconnected reading %s: %w", method, readErr)
		}
		var resp struct {
			ID     int             `json:"id"`
			Method string          `json:"method"` // 通知行有 method 无 id（id 解析为 0；本客户端请求 id 从 1 递增）
			Params json.RawMessage `json:"params"`
			Err    *struct {
				Message string `json:"message"`
			} `json:"error"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(line, &resp); err != nil {
			return nil, nil, fmt.Errorf("mcp unmarshal %s: %w", method, err)
		}
		if resp.Method != "" && resp.ID == 0 {
			notes = append(notes, mcpNotification{resp.Method, resp.Params})
			continue
		}
		if resp.Err != nil { ... }   // 原校验不变
		if resp.ID != id { ... }
		return resp.Result, notes, nil
	}
}
```

（注意所有 return 路径补齐三返回值。）

- [ ] **Step 4: 跑测试确认通过 + mcp 包回归 + Commit**

```bash
cd backend && go test ./internal/mcp/ -count=1 && go vet ./internal/mcp/ && gofmt -l internal/mcp/
git add backend/internal/mcp/client.go backend/internal/mcp/client_test.go
git commit -m "feat(mcp): client skips and dispatches JSON-RPC notifications"
```

### Task 3: manager relistTools 接线（TDD）

**Files:**
- Modify: `backend/internal/mcp/manager.go`
- Test: `backend/internal/mcp/manager_test.go`（追加）

- [ ] **Step 1: 写失败测试**（manager_test.go 追加）：

```go
// pushTransport：通知先于下一次响应吐出；tools/list 按队列依次返回扩容的工具集。
type pushTransport struct {
	mu     sync.Mutex
	tools  [][]string // 每次 tools/list 依次弹出（最后一个重复使用）
	inject [][]byte   // 在下一条响应前先吐出的通知行
}

func (t *pushTransport) WriteLine(p []byte) error { return nil }

func (t *pushTransport) ReadLine() ([]byte, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	// 无法直接拿 method——用 pending 方案太重；简化：inject 队列只影响一次读取。
	if len(t.inject) > 0 {
		line := t.inject[0]
		t.inject = t.inject[1:]
		return line, nil
	}
	return nil, fmt.Errorf("pushTransport: unexpected read")
}
func (t *pushTransport) Stop() {}
```

（此 transport 需按 method 响应——实现建议改为带 pending 请求的完整实现，参照 FakeTransport 结构复制 cannedFor 逻辑并加 inject 与 tools 队列；下方 Step 3 给出测试断言，transport 细节以编译通过、行为满足为准。）

测试断言：

```go
func TestToolsListChangedNotificationRefreshesStatus(t *testing.T) {
	pt := &pushTransport{tools: [][]string{{"read"}, {"read", "write"}}}
	f := &singleFactory{t: pt}
	m := NewManager(f)
	if err := m.SpawnAll(context.Background(), []ServerSpec{{Name: "fs", Command: "npx"}}); err != nil {
		t.Fatal(err)
	}
	if st := m.Status(); len(st) != 1 || len(st[0].Tools) != 1 {
		t.Fatalf("initial Status = %+v", st)
	}
	pt.mu.Lock()
	pt.inject = append(pt.inject, []byte(`{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}`))
	pt.mu.Unlock()
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
```

（`singleFactory` 若 manager_test 已有等价 helper 则复用；否则新建：`type singleFactory struct{ t Transport }` + `New(ctx, ServerSpec) (Transport, error) { return t.t, nil }`。）

- [ ] **Step 2: 跑测试确认失败**（relistTools undefined / tools 不刷新）。

- [ ] **Step 3: 实现 manager.go**

connect 成功分支（`m.clis[s.Name] = c` 之前）注册 handler：

```go
c.SetNotificationHandler(func(method string, params json.RawMessage) {
	if method != "notifications/tools/list_changed" {
		return
	}
	go m.relistTools(context.Background(), s)
})
```

新增方法与工具比较：

```go
// relistTools 在收到 tools/list_changed 通知后重列工具并刷新状态；
// 与进行中的 rpc 共享 client 锁（排队），工具集未变化时跳过写入。
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
```

- [ ] **Step 4: 跑测试确认通过 + mcp 全包回归 + Commit**

```bash
cd backend && go test ./internal/mcp/ -count=1 && go vet ./internal/mcp/ && gofmt -l internal/mcp/
git add backend/internal/mcp/manager.go backend/internal/mcp/manager_test.go
git commit -m "feat(mcp): tools/list_changed notification triggers immediate tool refresh"
```

---

## Chunk 3：批 3 evals 32 + 收尾

### Task 4: 两个会话 API 场景（TDD）

**Files:**
- Test: `backend/tests/evals/scenarios_round19_test.go`（新建）
- Modify: `backend/README.md` + `README.en.md`（evals 计数 30→32）

- [ ] **Step 1: 写场景**（参照 scenarios_round16_test.go 结构）：

```go
package evals

import (
	"strings"
	"testing"

	"rivus-agent-backend/tests/evals" // 若同包则省略
)

func TestScenarioSessionRenamePersists(t *testing.T) {
	Scenario{
		Name:     "session_rename_persists",
		Category: "tool",
		Goal:     "rename session via API",
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueText("好的，已收到。")
			return ""
		},
		Want: Expect{TerminalStatus: domain.RunSucceeded, MaxModelCalls: 1},
		PostCheck: func(t *testing.T, h *testutil.Harness, runID string, run domain.Run) {
			sid := run.SessionID
			code, body := h.Do("PATCH", "/api/v1/sessions/"+sid, map[string]any{"title": "renamed-eval"}, nil)
			if code != 200 || !strings.Contains(string(body), "renamed-eval") {
				t.Fatalf("rename: %d %s", code, body)
			}
			code, body = h.Do("GET", "/api/v1/sessions", nil, nil)
			if code != 200 || !strings.Contains(string(body), "renamed-eval") {
				t.Fatalf("get after rename: %d %s", code, body)
			}
			code, _ = h.Do("PATCH", "/api/v1/sessions/"+sid, map[string]any{"title": "   "}, nil)
			if code != 400 {
				t.Fatalf("empty title: %d", code)
			}
		},
	}.Run(t)
}

func TestScenarioSessionSearchFilters(t *testing.T) {
	Scenario{
		Name:     "session_search_filters",
		Category: "tool",
		Goal:     "search sessions via q param",
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueText("好的。")
			return ""
		},
		Want: Expect{TerminalStatus: domain.RunSucceeded, MaxModelCalls: 1},
		PostCheck: func(t *testing.T, h *testutil.Harness, runID string, run domain.Run) {
			h.CreateSession("r19-search-target-xyz")
			h.CreateSession("r19-search-other-abc")
			code, body := h.Do("GET", "/api/v1/sessions?q=target-xyz", nil, nil)
			if code != 200 {
				t.Fatalf("search: %d", code)
			}
			if !strings.Contains(string(body), "r19-search-target-xyz") {
				t.Fatalf("expected hit: %s", body)
			}
			if strings.Contains(string(body), "r19-search-other-abc") {
				t.Fatalf("unexpected hit: %s", body)
			}
			code, body = h.Do("GET", "/api/v1/sessions", nil, nil)
			if code != 200 || !strings.Contains(string(body), "r19-search-other-abc") {
				t.Fatalf("no-q must list all: %d %s", code, body)
			}
		},
	}.Run(t)
}
```

（import 以文件实际为准：`context` 不需要；`domain`/`testutil` 已是 evals 包既有依赖；同包则 Scenario 直接用。）

- [ ] **Step 2: 跑测试确认通过**

```bash
cd backend && go test ./tests/evals/ -run 'TestScenarioSession' -count=1 -v
```
（此批为纯新增断言，后端行为已被集成测试覆盖——预期直接绿；若 RED 则是场景代码问题，修正后 GREEN。）

- [ ] **Step 3: 全量 evals + README 计数**

```bash
cd backend && make test-eval
grep -n "30 个场景\|30 scenarios" backend/README.md README.en.md
```
backend/README.md 与 README.en.md 中 evals 小节的计数行改为 `32 个场景（工具级 21 + agent 级 11）` / `32 scenarios (21 tool-level + 11 agent-level)`，并在新增场景文件列表加 `scenarios_round19_test.go`。

- [ ] **Step 4: Commit**

```bash
git add backend/tests/evals/scenarios_round19_test.go backend/README.md README.en.md
git commit -m "test(evals): session rename and search scenarios (30 -> 32)"
```

### Task 5: 全量验收 + spec 偏差回写

- [ ] **Step 1: 后端全量**

```bash
cd backend && go build ./... && go vet ./... && go test ./... -count=1 -timeout 600s && make test-eval
```
期望：全绿；evals 32 pass_rate 1。

- [ ] **Step 2: 前端回归（零改动预期）**

```bash
cd frontend && npx tsc --noEmit && npm run lint && npx vitest run
```

- [ ] **Step 3: spec 偏差回写 + Commit**

docs/superpowers/specs/2026-10-29-round19-docs-mcp-notify-evals-design.md §7 追加执行偏差（pushTransport 实际形态、README 计数行实际位置、其他执行中发现的真实偏差）。

```bash
git add docs/superpowers/specs/2026-10-29-round19-docs-mcp-notify-evals-design.md
git commit -m "docs(spec): round 19 execution deviations"
git status --short   # 干净（.omo/ 除外）
```

---

## 自查笔记（计划评审，无 subagent）

1. **通知判型**：`method != "" && id == 0`——本客户端 id 从 1 递增（atomic.AddInt32 首值 1），通知行无 id 解析为 0，判型安全；响应缺 method。
2. **通知分发时机**：rpcLocked 返回后（c.mu 已释放）在外层 rpc 分发，回调重入不会死锁。
3. **relistTools 与进行中 rpc**：共享 client.mu 排队，符合 R8 记录的单 in-flight 架构约束。
4. **pushTransport**：计划中的简化版骨架不足以通过测试（ReadLine 无法知道 method）——实现时复制 FakeTransport 的 pending/cannedFor 模式并加 inject 队列 + tools 队列，测试断言不变。
5. **evals 场景为 PostCheck 断言型**（run 仅作载体）：与 R16 idempotency 场景同型，直接绿概率高；RED 时查 h.Do 路径与 PATCH 方法注册。
6. **README 计数行**：执行时先 grep 定位实际行再改，避免行号漂移。
