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
	"sync"
	"testing"
	"time"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	httpapi "rivus-agent-backend/internal/api/http"
	"rivus-agent-backend/internal/config"
	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/runtime"
	"rivus-agent-backend/internal/service"
	"rivus-agent-backend/internal/store"
	"rivus-agent-backend/internal/tool"
	"rivus-agent-backend/internal/tool/builtin"
)

type scriptState struct {
	mu    sync.Mutex
	queue []func(call int, input []*schema.Message) (*schema.Message, error)
	calls int
	tools []*schema.ToolInfo
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

func (m *ScriptedModel) Generate(_ context.Context, input []*schema.Message, _ ...einomodel.Option) (*schema.Message, error) {
	m.st.mu.Lock()
	defer m.st.mu.Unlock()
	m.st.calls++
	if len(m.st.queue) == 0 {
		return &schema.Message{Role: schema.Assistant, Content: "done"}, nil
	}
	fn := m.st.queue[0]
	m.st.queue = m.st.queue[1:]
	return fn(m.st.calls, input)
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

type Harness struct {
	T         *testing.T
	DB        *sql.DB
	Tasks     *store.TaskRepo
	Events    *store.EventRepo
	Approvals *store.ApprovalRepo
	Model     *ScriptedModel
	Server    *httptest.Server
	Client    *http.Client
}

func New(t *testing.T, extraTools ...tool.Definition) *Harness {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	tasks := store.NewTaskRepo(db)
	events := store.NewEventRepo(db)
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
	executor := tool.NewExecutor(registry)
	model := NewScriptedModel()
	runner := runtime.NewEinoRunner(func() (einomodel.ToolCallingChatModel, error) {
		return model, nil
	}, registry, executor, checkpoints)
	rt := runtime.New(runtime.Deps{Tasks: tasks, Events: events, Approvals: approvals, Runner: runner}, 4)

	cfg := config.Default()
	cfg.MaxDurationS = 120
	runs := service.NewRunService(cfg, tasks, events, approvals, rt, runner)
	sessions := service.NewSessionService(tasks)
	srv := httpapi.New(cfg, db, tasks, events, runs, sessions)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	return &Harness{T: t, DB: db, Tasks: tasks, Events: events, Approvals: approvals,
		Model: model, Server: ts, Client: ts.Client()}
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
