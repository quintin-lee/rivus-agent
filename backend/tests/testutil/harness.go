package testutil

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	httpapi "rivus-agent-backend/internal/api/http"
	"rivus-agent-backend/internal/config"
	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/eventshub"
	"rivus-agent-backend/internal/mcp"
	"rivus-agent-backend/internal/runtime"
	"rivus-agent-backend/internal/service"
	"rivus-agent-backend/internal/skill"
	"rivus-agent-backend/internal/store"
	"rivus-agent-backend/internal/subagent"
	"rivus-agent-backend/internal/tool"
	"rivus-agent-backend/internal/tool/builtin"
)

type scriptState struct {
	mu       sync.Mutex
	queue    []func(call int, input []*schema.Message) (*schema.Message, error)
	fallback func(call int, input []*schema.Message) (*schema.Message, error)
	calls    int
}

type ScriptedModel struct{ st *scriptState }

func NewScriptedModel() *ScriptedModel { return &ScriptedModel{st: &scriptState{}} }

func (m *ScriptedModel) EnqueueText(text string) {
	m.st.mu.Lock()
	defer m.st.mu.Unlock()
	m.st.queue = append(m.st.queue, func(int, []*schema.Message) (*schema.Message, error) {
		return &schema.Message{Role: schema.Assistant, Content: text}, nil
	})
}

func (m *ScriptedModel) EnqueueToolCall(id, name, args string) {
	m.st.mu.Lock()
	defer m.st.mu.Unlock()
	m.st.queue = append(m.st.queue, func(int, []*schema.Message) (*schema.Message, error) {
		return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
			ID: id, Type: "function",
			Function: schema.FunctionCall{Name: name, Arguments: args},
		}}}, nil
	})
}

func (m *ScriptedModel) EnqueueFunc(fn func(call int, input []*schema.Message) (*schema.Message, error)) {
	m.st.mu.Lock()
	defer m.st.mu.Unlock()
	m.st.queue = append(m.st.queue, fn)
}

func (m *ScriptedModel) Calls() int {
	m.st.mu.Lock()
	defer m.st.mu.Unlock()
	return m.st.calls
}

func (m *ScriptedModel) WithTools(tools []*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	return &ScriptedModel{st: m.st}, nil
}

func (m *ScriptedModel) SetFallback(fn func(call int, input []*schema.Message) (*schema.Message, error)) {
	m.st.mu.Lock()
	defer m.st.mu.Unlock()
	m.st.fallback = fn
}

func (m *ScriptedModel) Generate(_ context.Context, input []*schema.Message, _ ...einomodel.Option) (*schema.Message, error) {
	m.st.mu.Lock()
	defer m.st.mu.Unlock()
	m.st.calls++
	if len(m.st.queue) > 0 {
		fn := m.st.queue[0]
		m.st.queue = m.st.queue[1:]
		return fn(m.st.calls, input)
	}
	if m.st.fallback != nil {
		return m.st.fallback(m.st.calls, input)
	}
	return &schema.Message{Role: schema.Assistant, Content: "done"}, nil
}

func (m *ScriptedModel) Stream(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	sr, sw := schema.Pipe[*schema.Message](1)
	sw.Send(msg, nil)
	sw.Close()
	return sr, nil
}

type TB interface {
	Helper()
	Cleanup(func())
	TempDir() string
	Fatal(args ...any)
	Fatalf(format string, args ...any)
}

type Harness struct {
	T         TB
	DB        *sql.DB
	Tasks     *store.TaskRepo
	Events    *store.EventRepo
	Approvals *store.ApprovalRepo
	Model     *ScriptedModel
	Server    *httptest.Server
	Runner    *runtime.EinoRunner
	Client    *http.Client
	// savedToolConfigs records /settings/tools PUT payloads received by the
	// stub provider (NewToolConfigHarness only; nil for New).
	savedToolConfigs []httpapi.ToolConfigIn
}

// SavedToolConfigs returns the /settings/tools PUT payloads the stub provider
// recorded (NewToolConfigHarness only; nil for New).
func (h *Harness) SavedToolConfigs() []httpapi.ToolConfigIn { return h.savedToolConfigs }

func New(t TB, extraTools ...tool.Definition) *Harness {
	return buildHarness(t, extraTools, nil, false, false /*withHub*/)
}

// NewHubHarness builds a Harness with the eventshub wired into EventRepo
// (SetPublisher) and httpapi (WithEventHub) — needed to exercise the SSE
// push fast path. New() remains the no-hub (ticker-only) baseline.
func NewHubHarness(t TB, extraTools ...tool.Definition) *Harness {
	t.Helper()
	return buildHarness(t, extraTools, nil, false, true /*withHub*/)
}

// NewToolConfigHarness builds a Harness with the /settings/tools API surface
// wired to a stub provider that records every SaveTools payload, plus the
// extensible-tooling registry surface: a real skill invoke_skill (backed by
// a temp-dir demo skill) and a canned mcp__fake__echo definition (stub
// handler, external/always, no live MCP process).
func NewToolConfigHarness(t TB) *Harness {
	t.Helper()
	var h *Harness
	stub := &stubToolProvider{
		status: httpapi.ToolStatus{
			Skills: httpapi.SkillsStatus{Enabled: true,
				Available: []httpapi.SkillInfo{{Name: "demo", Description: "demo skill"}}},
			MCP: httpapi.MCPStatus{Enabled: true,
				Servers: []httpapi.MCPServerInfo{{Name: "fs", Status: "up", Command: "npx"}}},
		},
		save: func(in httpapi.ToolConfigIn) {
			h.savedToolConfigs = append(h.savedToolConfigs, in)
		},
	}
	h = buildHarness(t, nil, stub, true, false /*withHub*/)
	h.savedToolConfigs = []httpapi.ToolConfigIn{}
	return h
}

// stubToolProvider implements httpapi.ToolConfigProvider for tests: a status
// that round-trips saved toggles (mirrors the real App contract where
// SaveTools persists and GetToolsStatus reflects it) plus SaveTools payload
// recording; no real reconcile.
type stubToolProvider struct {
	status httpapi.ToolStatus
	save   func(httpapi.ToolConfigIn)
}

func (p *stubToolProvider) GetToolsStatus(context.Context) (httpapi.ToolStatus, error) {
	return p.status, nil
}

func (p *stubToolProvider) SaveTools(_ context.Context, in httpapi.ToolConfigIn) error {
	// 忠实模拟真 provider 契约：载荷校验失败 → 400（经 ErrInvalidToolConfig 包装）。
	specs := make([]mcp.ServerSpec, 0, len(in.MCPServers))
	for _, s := range in.MCPServers {
		var oauth *mcp.OAuthConfig
		if s.OAuth != nil {
			oauth = &mcp.OAuthConfig{TokenURL: s.OAuth.TokenURL, ClientID: s.OAuth.ClientID,
				ClientSecret: s.OAuth.ClientSecret, Scopes: s.OAuth.Scopes}
		}
		specs = append(specs, mcp.ServerSpec{Name: s.Name, Command: s.Command, Args: s.Args,
			Type: s.Type, URL: s.URL, Headers: s.Headers, OAuth: oauth})
	}
	if err := mcp.ValidateSpecs(specs); err != nil {
		return fmt.Errorf("%w: %v", httpapi.ErrInvalidToolConfig, err)
	}
	p.save(in)
	p.status.Skills.Enabled = in.SkillsEnabled
	p.status.MCP.Enabled = in.MCPEnabled
	return nil
}

func buildHarness(t TB, extraTools []tool.Definition, provider httpapi.ToolConfigProvider, registerToolConfigSurface bool, withHub bool) *Harness {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	tasks := store.NewTaskRepo(db)
	events := store.NewEventRepo(db)
	var hub *eventshub.Hub
	if withHub {
		hub = eventshub.New()
		events.SetPublisher(hub.Publish)
	}
	approvals := store.NewApprovalRepo(db)
	checkpoints := store.NewCheckpointStore(db)

	registry := tool.NewRegistry()
	if err := builtin.RegisterAll(registry); err != nil {
		t.Fatal(err)
	}
	for _, d := range extraTools {
		if err := registry.Register(d); err != nil {
			t.Fatal(err)
		}
	}
	if registerToolConfigSurface {
		// Real invoke_skill tool backed by a temp-dir demo skill fixture.
		skillDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(skillDir, "demo"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skillDir, "demo", "SKILL.md"),
			[]byte("description: demo skill\nversion: v1\n\n# demo\nDEMO SKILL BODY\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		// references/ 子文件：invoke_skill reference 参数的评估覆盖（R14）。
		if err := os.MkdirAll(filepath.Join(skillDir, "demo", "references"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skillDir, "demo", "references", "cheatsheet.md"),
			[]byte("# cheatsheet\nR14-CHEATSHEET-MARKER\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		loader := skill.NewLoader(skillDir)
		_ = loader.Refresh()
		if err := registry.Register(skill.InvokeSkill(loader, 32*1024)); err != nil {
			t.Fatal(err)
		}
		// Canned mcp__ tool: deterministic stub handler, no live MCP process.
		// RequiredScopes MUST stay empty (§4.3 invariant); external + always
		// routes it to the approval gate.
		if err := registry.Register(tool.Definition{
			Name:           "mcp__fake__echo",
			Description:    "fake MCP echo tool (canned test stub)",
			Version:        "v1",
			InputSchema:    `{"type":"object","properties":{"msg":{"type":"string"}},"required":["msg"]}`,
			Risk:           tool.RiskExternal,
			Approval:       tool.ApprovalAlways,
			TimeoutMs:      5000,
			MaxResultBytes: 4096,
			Handler: func(context.Context, json.RawMessage) (string, error) {
				return "\"echoed\"", nil
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	executor := tool.NewExecutor(registry)
	model := NewScriptedModel()
	runner := runtime.NewEinoRunner(func() (einomodel.ToolCallingChatModel, error) {
		return model, nil
	}, registry, executor, checkpoints)
	// task 工具（镜像 app.go）：runner 同时满足 ChildExecutor + CheckpointCleaner。
	if err := registry.Register(subagent.TaskTool(registry, tasks, runner, runner)); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(subagent.TasksTool(registry, tasks, runner, runner)); err != nil {
		t.Fatal(err)
	}
	// memory 工具（镜像 app.go：会话级持久记忆）。
	memories := store.NewMemoryRepo(db)
	for _, d := range builtin.MemoryTools(memories) {
		if err := registry.Register(d); err != nil {
			t.Fatal(err)
		}
	}
	rt := runtime.New(runtime.Deps{Tasks: tasks, Events: events, Approvals: approvals, Runner: runner, Memories: memories}, 4)

	cfg := config.Default()
	cfg.MaxDurationS = 120
	runs := service.NewRunService(cfg, tasks, events, approvals, rt, runner)
	sessions := service.NewSessionService(tasks)
	settings := store.NewSettingsRepo(db)
	var opts []httpapi.Option
	if provider != nil {
		opts = append(opts, httpapi.WithToolConfigProvider(provider))
	}
	if hub != nil {
		opts = append(opts, httpapi.WithEventHub(hub))
	}
	srv := httpapi.New(cfg, db, tasks, events, settings, runs, sessions, opts...)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	return &Harness{T: t, DB: db, Tasks: tasks, Events: events, Approvals: approvals,
		Model: model, Runner: runner, Server: ts, Client: ts.Client()}
}

func (h *Harness) Do(method, path string, body any, headers map[string]string) (int, []byte) {
	h.T.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			h.T.Fatal(err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, h.Server.URL+path, rdr)
	if err != nil {
		h.T.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.Client.Do(req)
	if err != nil {
		h.T.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func (h *Harness) CreateSession(title string) string {
	h.T.Helper()
	code, body := h.Do("POST", "/api/v1/sessions", map[string]string{"title": title}, nil)
	if code != 201 {
		h.T.Fatalf("create session: %d %s", code, body)
	}
	var out map[string]string
	if err := json.Unmarshal(body, &out); err != nil {
		h.T.Fatal(err)
	}
	return out["session_id"]
}

func (h *Harness) CreateRun(sessionID string, payload map[string]any, idemKey string) (string, int, map[string]any) {
	h.T.Helper()
	payload["session_id"] = sessionID
	code, body := h.Do("POST", "/api/v1/runs", payload, map[string]string{"Idempotency-Key": idemKey})
	var out map[string]any
	_ = json.Unmarshal(body, &out)
	id, _ := out["run_id"].(string)
	return id, code, out
}

func (h *Harness) GetRun(id string) (domain.Run, []domain.Step) {
	h.T.Helper()
	code, body := h.Do("GET", "/api/v1/runs/"+id, nil, nil)
	if code != 200 {
		h.T.Fatalf("get run: %d %s", code, body)
	}
	var out struct {
		Run   domain.Run    `json:"run"`
		Steps []domain.Step `json:"steps"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		h.T.Fatal(err)
	}
	return out.Run, out.Steps
}

func (h *Harness) WaitTerminal(id string, timeout time.Duration) domain.Run {
	h.T.Helper()
	deadline := time.Now().Add(timeout)
	for {
		run, _ := h.GetRun(id)
		if run.Status.Terminal() || run.Status == domain.RunWaitingApproval {
			return run
		}
		if time.Now().After(deadline) {
			h.T.Fatalf("run %s not terminal after %v (status=%s)", id, timeout, run.Status)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func MustParse[T any](t *testing.T, b []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

var _ = fmt.Sprint
