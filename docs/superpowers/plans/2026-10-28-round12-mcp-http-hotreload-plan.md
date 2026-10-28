# Round 12 MCP 深化（HTTP 传输 + config.yaml 热载）Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:executing-plans to implement this plan (user constraint: NO subagents). Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** MCP 支持 HTTP(S) 远程服务器（Streamable-HTTP JSON/SSE 响应）+ config.yaml `mcp:` 段（启动写穿 settings）+ 5s 轮询文件热载。

**Architecture:** 扩展 `ServerSpec{Type,URL,Headers}` 与 Transport 工厂按 Type 分发；`httpTransport` 一次 POST 一次读，与 `client.rpc` 同步模型及 Manager 30s 超时复用；config 包新增镜像结构 `MCPConfig`（不反向依赖 mcp）；`applyMCPConfig` 统一"写穿 settings → Reconcile → registry 刷新"，启动合并与热载共用。

**Tech Stack:** Go 1.27 stdlib（net/http、encoding/json、os.Stat 轮询）；零新依赖。Spec: `docs/superpowers/specs/2026-10-28-round12-mcp-http-hotreload-design.md`。

**自审备注（执行前已核对源码）**：
- SSE id 匹配简化为"返回首个含 `id` 字段的 data 载荷"——`client.rpc` 本身校验 id 并在 c.mu 串行下同一时刻只有一个 in-flight 请求，transport 无需记录请求 id。
- `MCPServerInfo`/`StatusEntry` 回显 **不含 headers**（spec §4.2 防 token 泄漏）。
- PUT 路径 `mcpServersFromIn` 目前无校验——本次加 `ValidateSpecs`（与 `ServersFromJSON` 共用），http 必须 URL http/https。
- app.Start(ctx) 持 main 的 signal ctx；watcher goroutine 用同一 ctx，Stop 无需改动。
- `applyMCPConfig` 放新文件 `internal/app/hotreload.go`（app.go 已 177 行）。
- workdir 重置：git add 用仓库根相对路径；go 命令用 `workdir=backend`。

---

## Task 1: ServerSpec 扩展 + 校验 + Spec 工厂分发

**Files:**
- Modify: `backend/internal/mcp/config.go`（ServerSpec + ServersFromJSON + 新 ValidateSpecs）
- Modify: `backend/internal/mcp/transport.go`（NewSpecTransportFactory）
- Modify: `backend/internal/mcp/manager.go`（StatusEntry += Type/URL）
- Test: `backend/internal/mcp/config_test.go`、`backend/internal/mcp/transport_test.go`

- [ ] **Step 1: 失败测试**（追加到 config_test.go）

```go
func TestServersFromJSONHTTPType(t *testing.T) {
	specs, err := ServersFromJSON(`[{"name":"remote","type":"http","url":"https://mcp.example.com/v1","headers":{"Authorization":"Bearer x"}}]`)
	if err != nil || len(specs) != 1 {
		t.Fatalf("err=%v specs=%v", err, specs)
	}
	if specs[0].Type != "http" || specs[0].URL != "https://mcp.example.com/v1" || specs[0].Headers["Authorization"] != "Bearer x" {
		t.Fatalf("spec=%+v", specs[0])
	}
	// http 无 URL → 拒绝；ws/wss → 拒绝
	if _, err := ServersFromJSON(`[{"name":"bad","type":"http"}]`); err == nil {
		t.Fatal("http without url must fail")
	}
	if _, err := ServersFromJSON(`[{"name":"bad","type":"http","url":"ws://x"}]`); err == nil {
		t.Fatal("ws scheme must fail")
	}
	// 空 type 仍按 stdio 校验（向后兼容）
	if _, err := ServersFromJSON(`[{"name":"s"}]`); err == nil {
		t.Fatal("stdio without command must fail")
	}
}

func TestValidateSpecs(t *testing.T) {
	ok := []ServerSpec{{Name: "a", Command: "x"}, {Name: "b", Type: "http", URL: "http://127.0.0.1:1"}}
	if err := ValidateSpecs(ok); err != nil {
		t.Fatalf("ok: %v", err)
	}
	bad := []ServerSpec{{Name: "c", Type: "http"}}
	if err := ValidateSpecs(bad); err == nil {
		t.Fatal("http spec without url must fail")
	}
}
```

（追加到 transport_test.go）

```go
func TestSpecFactoryDispatch(t *testing.T) {
	f := NewSpecTransportFactory()
	if _, err := f.New(context.Background(), ServerSpec{Name: "x", Command: "/nonexistent-cmd-xyz"}); err == nil {
		t.Fatal("stdio dispatch expected exec failure")
	}
	if _, err := f.New(context.Background(), ServerSpec{Name: "x", Type: "http", URL: "http://127.0.0.1:1"}); err != nil {
		t.Fatalf("http dispatch must not dial eagerly: %v", err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**：`cd backend && go test ./internal/mcp/ -count=1` → 编译错（Type/URL/Headers 字段、NewSpecTransportFactory、ValidateSpecs 未定义）
- [ ] **Step 3: 实现**

config.go：ServerSpec 加字段

```go
// ServerSpec is one configured MCP server: stdio (child process) or http (remote).
type ServerSpec struct {
	Name    string            `json:"name"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Type    string            `json:"type,omitempty"` // "" | "stdio" | "http"
	URL     string            `json:"url,omitempty"`  // http(s) endpoint for type=http
	Headers map[string]string `json:"headers,omitempty"`
}
```

ServersFromJSON 校验改为调 `ValidateSpecs(out)`；新函数：

```go
// ValidateSpecs checks name/command/url rules for a spec list.
func ValidateSpecs(specs []ServerSpec) error {
	for _, s := range specs {
		if !ValidServerName(s.Name) {
			return fmt.Errorf("server %q: invalid name", s.Name)
		}
		switch s.Type {
		case "", "stdio":
			if s.Command == "" {
				return fmt.Errorf("server %q: empty command", s.Name)
			}
		case "http":
			u, err := url.Parse(s.URL)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return fmt.Errorf("server %q: invalid http url %q", s.Name, s.URL)
			}
		default:
			return fmt.Errorf("server %q: unknown type %q", s.Name, s.Type)
		}
	}
	return nil
}
```

（config.go import += `net/url`）

transport.go 末尾追加：

```go
// SpecTransportFactory dispatches on spec.Type: http → remote HTTP transport,
// otherwise stdio child process.
type SpecTransportFactory struct {
	HTTP *HTTPTransportFactory
}

func NewSpecTransportFactory() *SpecTransportFactory { return &SpecTransportFactory{HTTP: NewHTTPTransportFactory()} }

func (f *SpecTransportFactory) New(ctx context.Context, spec ServerSpec) (Transport, error) {
	if spec.Type == "http" {
		return f.HTTP.New(ctx, spec)
	}
	return NewStdioTransport(ctx, spec.Command, spec.Args)
}
```

manager.go StatusEntry 加 `Type string json:"type,omitempty"` + `URL string json:"url,omitempty"`（记录处：`rememberSpecs`/connect 成功写 status 时带上 spec.Type/spec.URL —— 查 manager.go 中 status 赋值的两处）。

- [ ] **Step 4: 跑测试通过** + `go build ./... && go vet ./internal/mcp/`
- [ ] **Step 5: Commit** `feat(mcp): ServerSpec type/url/headers, validation, spec-dispatching transport factory`

## Task 2: httpTransport（JSON + SSE 响应）

**Files:**
- Create: `backend/internal/mcp/http.go`
- Test: `backend/internal/mcp/http_test.go`

- [ ] **Step 1: 失败测试**

```go
package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// mcpJSONServer 回 JSON：initialize / tools/list / tools/call 全应答。
func mcpJSONServer(t *testing.T, gotHeaders *map[string]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotHeaders = map[string]string{"Authorization": r.Header.Get("Authorization"), "Accept": r.Header.Get("Accept")}
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2024-11-05"}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{"name": "echo", "description": "d"}}}
		case "tools/call":
			result = map[string]any{"content": []map[string]any{{"type": "text", "text": "pong"}}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
}

func TestHTTPTransportJSONRoundtrip(t *testing.T) {
	var got map[string]string
	srv := mcpJSONServer(t, &got)
	defer srv.Close()
	tr, err := NewHTTPTransport(context.Background(), ServerSpec{Name: "r", Type: "http", URL: srv.URL, Headers: map[string]string{"Authorization": "Bearer t0k"}})
	if err != nil {
		t.Fatal(err)
	}
	c := NewClient(tr)
	if err := c.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if got["Authorization"] != "Bearer t0k" || !strings.Contains(got["Accept"], "text/event-stream") {
		t.Fatalf("headers=%v", got)
	}
	tools, err := c.ListTools(context.Background())
	if err != nil || len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools=%v err=%v", tools, err)
	}
	out, err := c.Call(context.Background(), "echo", nil)
	if err != nil || out != "pong" {
		t.Fatalf("call=%q err=%v", out, err)
	}
}

func TestHTTPTransportSSEResponse(t *testing.T) {
	var id atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		id.Store(int64(req.ID))
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = w.Write([]byte(": keepalive\n\n"))
		flusher.Flush()
		payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID,
			"result": map[string]any{"content": []map[string]any{{"type": "text", "text": "sse-pong"}}}})
		_, _ = w.Write([]byte("event: message\ndata: " + string(payload) + "\n\n"))
		flusher.Flush()
	}))
	defer srv.Close()
	tr, _ := NewHTTPTransport(context.Background(), ServerSpec{Name: "r", Type: "http", URL: srv.URL})
	out, err := NewClient(tr).Call(context.Background(), "echo", nil)
	if err != nil || out != "sse-pong" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestHTTPTransportErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "denied", http.StatusUnauthorized)
	}))
	defer srv.Close()
	tr, _ := NewHTTPTransport(context.Background(), ServerSpec{Name: "r", Type: "http", URL: srv.URL})
	c := NewClient(tr)
	if err := c.Initialize(context.Background()); err == nil {
		t.Fatal("401 must fail")
	}
}
```

- [ ] **Step 2: 确认失败**（NewHTTPTransport 未定义）
- [ ] **Step 3: 实现 `http.go`**

```go
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// HTTPTransportFactory builds HTTP transports for remote MCP servers.
type HTTPTransportFactory struct{ client *http.Client }

func NewHTTPTransportFactory() *HTTPTransportFactory {
	return &HTTPTransportFactory{client: &http.Client{Timeout: httpCallTimeout}}
}

func (f *HTTPTransportFactory) New(_ context.Context, spec ServerSpec) (Transport, error) {
	return newHTTPTransport(f.client, spec)
}

const httpCallTimeout = 35 * time.Second // > Manager 30s call timeout

// httpTransport: WriteLine POSTs the JSON-RPC request; ReadLine returns the
// full response — JSON body as-is, or the matching data payload of an
// text/event-stream reply. One write + one read per rpc, matching client.rpc.
type httpTransport struct {
	spec   ServerSpec
	client *http.Client
	resp   *http.Response
	body   []byte
	read   bool
}

func newHTTPTransport(client *http.Client, spec ServerSpec) (*httpTransport, error) {
	return &httpTransport{spec: spec, client: client}, nil
}

func (t *httpTransport) WriteLine(p []byte) error {
	req, err := http.NewRequest(http.MethodPost, t.spec.URL, bytes.NewReader(p))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range t.spec.Headers {
		req.Header.Set(k, v)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		return fmt.Errorf("mcp http %s: status %d: %s", t.spec.Name, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	t.resp = resp
	t.read = false
	return nil
}

func (t *httpTransport) ReadLine() ([]byte, error) {
	if t.resp == nil {
		return nil, fmt.Errorf("mcp http %s: read without write", t.spec.Name)
	}
	if t.read {
		t.resp.Body.Close()
		t.resp = nil
		return nil, io.EOF
	}
	t.read = true
	body, err := io.ReadAll(io.LimitReader(t.resp.Body, 1<<20))
	ctype := t.resp.Header.Get("Content-Type")
	if err != nil {
		t.resp.Body.Close()
		return nil, err
	}
	if strings.HasPrefix(ctype, "text/event-stream") {
		data, derr := sseResponseData(body)
		if derr != nil {
			t.resp.Body.Close()
			return nil, derr
		}
		return data, nil
	}
	return body, nil // trailing io.EOF surfaced on NEXT ReadLine via t.read
}

// sseResponseData scans SSE frames and returns the first data payload that
// parses as a JSON object with an "id" field (client.rpc re-validates id).
func sseResponseData(body []byte) ([]byte, error) {
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var data strings.Builder
	inData := false
	flush := func() ([]byte, error) {
		if !inData {
			return nil, nil
		}
		payload := []byte(data.String())
		data.Reset()
		inData = false
		var probe struct {
			ID int `json:"id"`
		}
		if json.Unmarshal(payload, &probe) == nil && probe.ID != 0 {
			return payload, nil
		}
		return nil, nil
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "": // frame boundary
			if p, _ := flush(); p != nil {
				return p, nil
			}
		case strings.HasPrefix(line, "data:"):
			if inData {
				data.WriteString("\n") // multi-line data joined by \n per SSE spec
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			inData = true
		}
	}
	if p, _ := flush(); p != nil {
		return p, nil
	}
	return nil, fmt.Errorf("mcp http: no json-rpc response in sse stream")
}

func (t *httpTransport) Stop() error {
	if t.resp != nil {
		t.resp.Body.Close()
		t.resp = nil
	}
	t.client.CloseIdleConnections()
	return nil
}
```

（http.go import += `time`）

- [ ] **Step 4: 跑测试通过**（mcp 包全绿）
- [ ] **Step 5: Commit** `feat(mcp): HTTP transport with JSON and SSE response handling`

## Task 3: API 透传 + app 接线

**Files:**
- Modify: `backend/internal/api/http/server.go`（MCPServerIn/MCPServerInfo += type/url/headers-in）
- Modify: `backend/internal/app/toolconfig.go`（mcpServersFromIn 映射 + ValidateSpecs；GetToolsStatus 回显 type/url 不回显 headers）
- Modify: `backend/internal/app/app.go:113`（`NewSpecTransportFactory`）
- Test: `backend/tests/integration/tool_settings_test.go`（既有文件追加或新建，看现有 settings 测试位置——grep `ToolConfigIn` 找既有测试文件追加）

- [ ] **Step 1: 失败测试**（integration 包；stub provider 场景下断言 payload 往返）

```go
func TestToolConfigHTTPMCPServerRoundtrip(t *testing.T) {
	h := testutil.New(t)
	body := map[string]any{
		"skillsEnabled": true, "mcpEnabled": true,
		"mcpServers": []map[string]any{
			{"name": "remote", "type": "http", "url": "http://127.0.0.1:9/mcp", "headers": map[string]string{"Authorization": "Bearer k"}},
		},
	}
	code, _ := h.Do("PUT", "/api/v1/settings/tools", body, nil)
	if code != 200 {
		t.Fatalf("PUT code=%d", code)
	}
	// GET 回显 type/url，不含 headers
	code, raw := h.Do("GET", "/api/v1/settings/tools", nil, nil)
	if code != 200 {
		t.Fatalf("GET code=%d", code)
	}
	s := string(raw)
	if !strings.Contains(s, `"remote"`) || !strings.Contains(s, `http://127.0.0.1:9/mcp`) {
		t.Fatalf("status missing url: %s", s)
	}
	if strings.Contains(s, "Bearer k") {
		t.Fatal("headers must not be echoed")
	}
}
```

（注意：stub provider 的 GetToolsStatus 从 `manager.Status()` 读——harness 里 manager 无服务器，`servers` 数组来自 status 而非 settings。若 stub 不回显，则退而断言 PUT 200 + GET 不含 headers 即可，url 断言放到 Task 5 集成。执行时按实际行为调整断言，在偏差中记录。）

- [ ] **Step 2: 确认失败**（MCPServerIn 无 Type 字段）
- [ ] **Step 3: 实现**

server.go：

```go
type MCPServerIn struct {
	Name    string            `json:"name"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Type    string            `json:"type,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

type MCPServerInfo struct {
	Name    string     `json:"name"`
	Status  string     `json:"status"`
	Command string     `json:"command,omitempty"`
	Args    []string   `json:"args,omitempty"`
	Type    string     `json:"type,omitempty"`
	URL     string     `json:"url,omitempty"`
	Tools   []ToolInfo `json:"tools"`
}
```

toolconfig.go `mcpServersFromIn`：映射新字段 + `return specs, mcp.ValidateSpecs(specs)`；`GetToolsStatus` 的 MCPServerInfo 填 `Type: e.Type, URL: e.URL`（headers 永不回显）。

app.go L113：`mcp.NewManager(mcp.NewSpecTransportFactory())`。

- [ ] **Step 4: 跑测试通过** + `go build ./... && go vet ./...`
- [ ] **Step 5: Commit** `feat(mcp): type/url/headers through settings API, spec factory wired in app`

## Task 4: config.yaml `mcp:` 段 + 启动写穿

**Files:**
- Modify: `backend/internal/config/config.go`（Config.MCP + MCPConfig/MCPServerYaml）
- Modify: `backend/internal/app/hotreload.go`（新建：applyMCPConfig + 启动合并调用点在 app.go）
- Test: `backend/internal/config/config_test.go` 追加

- [ ] **Step 1: 失败测试**

```go
func TestMCPConfigYAML(t *testing.T) {
	yaml := "addr: ':0'\nmcp:\n  enabled: true\n  servers:\n    - name: fs\n      command: npx\n      args: ['-y', '@x/fs']\n    - name: remote\n      type: http\n      url: https://mcp.example.com\n      headers:\n        Authorization: Bearer k\n"
	cfg, err := Load(yamlFromReader(t, yaml))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MCP == nil || !cfg.MCP.Enabled || len(cfg.MCP.Servers) != 2 {
		t.Fatalf("mcp=%+v err=%v", cfg.MCP, err)
	}
	if cfg.MCP.Servers[1].Type != "http" || cfg.MCP.Servers[1].Headers["Authorization"] != "Bearer k" {
		t.Fatalf("remote=%+v", cfg.MCP.Servers[1])
	}
	// 无 mcp 段 → nil
	cfg2, err := Load(yamlFromReader(t, "addr: ':0'\n"))
	if err != nil || cfg2.MCP != nil {
		t.Fatalf("mcp=%+v err=%v", cfg2.MCP, err)
	}
}
```

（查 config_test.go 现有临时 yaml 写法复用；无则加 yamlFromReader helper：t.TempDir 写文件返回路径。）

- [ ] **Step 2: 确认失败**（Config.MCP 未定义）
- [ ] **Step 3: 实现**

config.go：

```go
// MCPConfig 是 config.yaml 的 mcp 段（nil = 未配置，settings 权威）。
type MCPConfig struct {
	Enabled bool            `yaml:"enabled"`
	Servers []MCPServerYaml `yaml:"servers"`
}

// MCPServerYaml 镜像 mcp.ServerSpec（config 包不 import mcp，避免反向依赖）。
type MCPServerYaml struct {
	Name    string            `yaml:"name"`
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args"`
	Type    string            `yaml:"type"`
	URL     string            `yaml:"url"`
	Headers map[string]string `yaml:"headers"`
}
```

Config 加 `MCP *MCPConfig yaml:"mcp,omitempty"`。Default() 不设置。

hotreload.go：

```go
package app

// applyMCPConfig writes the given MCP switch+servers through to settings and
// reconciles live connections + the shared registry (spec: yaml 有段=权威).
func (a *App) applyMCPConfig(ctx context.Context, settings *store.SettingsRepo,
	registry *tool.Registry, enabled bool, servers []mcp.ServerSpec) error {
	if err := settings.Set(ctx, mcp.KeyMCPEnabled, boolStr(enabled)); err != nil {
		return err
	}
	if err := settings.Set(ctx, mcp.KeyMCP_servers, marshalJSON(servers)); err != nil {
		return err
	}
	live := servers
	if !enabled {
		live = nil
	}
	if err := a.manager.Reconcile(ctx, live); err != nil {
		return err
	}
	return registry.Replace("mcp__", mcp.MCPDefinitions(a.manager.Status(), a.manager))
}

func mcpSpecsFromYaml(in []config.MCPServerYaml) []mcp.ServerSpec {
	out := make([]mcp.ServerSpec, 0, len(in))
	for _, s := range in {
		out = append(out, mcp.ServerSpec{Name: s.Name, Command: s.Command, Args: s.Args, Type: s.Type, URL: s.URL, Headers: s.Headers})
	}
	return out
}
```

app.go Start 中 `settings := store.NewSettingsRepo(db)` 之后、`ReadToolConfig` 之前：

```go
	// config.yaml 有 mcp 段 = yaml 权威：写穿 settings（每次启动重放）。
	if cfg.MCP != nil {
		if err := a.applyMCPConfig(ctx, settings, registry, cfg.MCP.Enabled, mcpSpecsFromYaml(cfg.MCP.Servers)); err != nil {
			a.log.Warn("mcp config apply failed", "err", err)
		}
	}
```

（若 yaml 校验失败——ValidateSpecs —— Warn 并继续走 settings 现状；把 ValidateSpecs 检查放 applyMCPConfig 入口。）

- [ ] **Step 4: 跑测试通过** + `go build ./... && go vet ./...`
- [ ] **Step 5: Commit** `feat(config): yaml mcp section applied through to settings on startup`

## Task 5: 文件监听热载

**Files:**
- Modify: `backend/internal/app/hotreload.go`（watcher）
- Modify: `backend/internal/app/app.go`（App.cfgPath 字段 + New 存 cfgPath + Start 启动 watcher）
- Test: `backend/internal/app/hotreload_test.go`

- [ ] **Step 1: 失败测试**

```go
func TestHotReloadAppliesMCPSection(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	writeCfg := func(content string) {
		if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 依赖 DB：用临时文件库建 settings+manager+registry 最小环境
	db, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	settings := store.NewSettingsRepo(db)
	registry := tool.NewRegistry()
	mgr := mcp.NewManager(mcp.NewFakeTransportFactory()) // 查 fake 工厂实际名，manager_test 里有
	a := &App{log: slog.New(slog.NewTextHandler(io.Discard, nil)), manager: mgr}

	writeCfg("mcp:\n  enabled: false\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.watchConfigFile(ctx, cfgPath, settings, registry, 20*time.Millisecond)

	time.Sleep(60 * time.Millisecond)
	writeCfg("mcp:\n  enabled: true\n  servers:\n    - name: echo\n      command: demo\n")
	// 轮询等待 settings 生效（≤2s）
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		v, ok, _ := settings.Get(ctx, mcp.KeyMCPEnabled)
		if ok && v == "true" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("hot reload did not apply mcp.enabled")
}
```

（fake 工厂名以 manager_test.go 现状为准；watchConfigFile 签名带 interval 便于注入。）

- [ ] **Step 2: 确认失败**（watchConfigFile 未定义）
- [ ] **Step 3: 实现**（hotreload.go 追加）

```go
// watchConfigFile polls cfgPath every interval; on mtime/size change it
// reloads config and applies the mcp section (yaml-权威 semantics). Errors
// are logged and keep the previous state.
func (a *App) watchConfigFile(ctx context.Context, cfgPath string,
	settings *store.SettingsRepo, registry *tool.Registry, interval time.Duration) {
	type stamp struct{ mod time.Time; size int64 }
	stat := func() (stamp, bool) {
		fi, err := os.Stat(cfgPath)
		if err != nil {
			return stamp{}, false
		}
		return stamp{fi.ModTime(), fi.Size()}, true
	}
	last, ok := stat()
	if !ok {
		return
	}
	tk := time.NewTicker(interval)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			cur, ok := stat()
			if !ok || (cur.mod == last.mod && cur.size == last.size) {
				continue
			}
			last = cur
			cfg, err := config.Load(cfgPath)
			if err != nil {
				a.log.Warn("mcp hot reload: config load failed", "err", err)
				continue
			}
			if cfg.MCP == nil {
				continue // 无 mcp 段不动 settings（settings 权威）
			}
			specs := mcpSpecsFromYaml(cfg.MCP.Servers)
			if err := mcp.ValidateSpecs(specs); err != nil {
				a.log.Warn("mcp hot reload: invalid specs ignored", "err", err)
				continue
			}
			if err := a.applyMCPConfig(ctx, settings, registry, cfg.MCP.Enabled, specs); err != nil {
				a.log.Warn("mcp hot reload: apply failed", "err", err)
				continue
			}
			a.log.Info("mcp config reloaded", "servers", len(specs), "enabled", cfg.MCP.Enabled)
		}
	}
}
```

app.go：App += `cfgPath string`；New 里 `&App{cfgPath: cfgPath, ...}`；Start 中 MCP 块之后：

```go
	if a.cfgPath != "" && cfg.MCP != nil {
		go a.watchConfigFile(ctx, a.cfgPath, settings, registry, 5*time.Second)
	}
```

- [ ] **Step 4: 跑测试通过** + app 包全绿
- [ ] **Step 5: Commit** `feat(app): poll-based hot reload of yaml mcp section`

## Task 6: 文档 + 全量验收

**Files:**
- Modify: `backend/README.md` + `backend/README.en.md`（MCP 段：HTTP 传输 type/url/headers、yaml mcp 段、热载语义、headers 不回显）
- Modify: `docs/superpowers/specs/2026-10-28-round12-mcp-http-hotreload-design.md`（执行偏差回写）

- [ ] **Step 1: README zh/en 更新**（MCP 小节追加 3-4 句：http type 示例 PUT payload、yaml 示例、热载 5s 轮询 + yaml 有段=接管语义）
- [ ] **Step 2: spec 偏差回写**（Task 3 stub 断言调整、fake 工厂实际名、Task 1 status 记录点等执行中发现的事实）
- [ ] **Step 3: 全量验收**

```bash
cd backend && go build ./... && go vet ./... && go test ./... -count=1 -timeout 600s
cd backend && make test-eval        # 20 scenarios pass_rate 1
cd backend && ~/go/bin/golangci-lint run ./...   # 0 issues
cd frontend && npx tsc --noEmit && npx vitest run   # 25 files / 157 tests（零漂移）
```

- [ ] **Step 4: Commit** `docs(round12): MCP http transport + yaml hot reload notes (zh/en)`
- [ ] **Step 5: wrap-up report**（批次表 + 证据 + push 提示：R6-R12 本地）
