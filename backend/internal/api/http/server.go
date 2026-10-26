package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"expvar"
	"net/http"
	"strconv"
	"strings"
	"time"

	"rivus-agent-backend/internal/config"
	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/eventshub"
	"rivus-agent-backend/internal/service"
	"rivus-agent-backend/internal/store"
)

// SSE follow 循环参数；单测可覆盖为小值。
var (
	followPollInterval   = 1 * time.Second
	followHeartbeatEvery = 15 * time.Second
	followMaxDuration    = 5 * time.Minute
)

type Server struct {
	cfg      config.Config
	db       *sql.DB
	tasks    *store.TaskRepo
	events   *store.EventRepo
	settings *store.SettingsRepo
	memories *store.MemoryRepo
	runs     *service.RunService
	sessions *service.SessionService
	tools    ToolConfigProvider
	hub      *eventshub.Hub
	mux      *http.ServeMux
}

// ToolConfigProvider backs the /settings/tools endpoints. The httpapi layer
// does not import internal/mcp or internal/skill — the App supplies this impl.
type ToolConfigProvider interface {
	GetToolsStatus(ctx context.Context) (ToolStatus, error)
	SaveTools(ctx context.Context, in ToolConfigIn) error
}

// ToolStatus is the GET /settings/tools payload (per-server shape mirrors
// mcp.StatusEntry, but httpapi defines its own type to stay decoupled).
type ToolStatus struct {
	Skills SkillsStatus `json:"skills"`
	MCP    MCPStatus    `json:"mcp"`
}

type SkillsStatus struct {
	Enabled   bool        `json:"enabled"`
	Available []SkillInfo `json:"available"`
}

type SkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type MCPStatus struct {
	Enabled bool            `json:"enabled"`
	Servers []MCPServerInfo `json:"servers"`
}

type MCPServerInfo struct {
	Name    string     `json:"name"`
	Status  string     `json:"status"` // "up" | "down" | "error"
	Command string     `json:"command"`
	Args    []string   `json:"args,omitempty"`
	Tools   []ToolInfo `json:"tools"`
}

type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ToolConfigIn is the PUT /settings/tools payload.
type ToolConfigIn struct {
	SkillsEnabled bool          `json:"skillsEnabled"`
	MCPEnabled    bool          `json:"mcpEnabled"`
	MCPServers    []MCPServerIn `json:"mcpServers"`
}

type MCPServerIn struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

// Option configures an httpapi.Server. Existing 7-arg New(...) calls stay
// valid; opt in to the tools API via WithToolConfigProvider(...).
type Option func(*serverOptions)

type serverOptions struct {
	tools ToolConfigProvider
	hub   *eventshub.Hub
}

func WithToolConfigProvider(p ToolConfigProvider) Option {
	return func(o *serverOptions) { o.tools = p }
}
func WithEventHub(hub *eventshub.Hub) Option {
	return func(o *serverOptions) { o.hub = hub }
}

func New(cfg config.Config, db *sql.DB, tasks *store.TaskRepo, events *store.EventRepo, settings *store.SettingsRepo, runs *service.RunService, sessions *service.SessionService, opts ...Option) *Server {
	o := serverOptions{}
	for _, opt := range opts {
		opt(&o)
	}
	s := &Server{cfg: cfg, db: db, tasks: tasks, events: events, settings: settings, runs: runs, sessions: sessions, tools: o.tools, hub: o.hub, mux: http.NewServeMux()}
	if db != nil {
		s.memories = store.NewMemoryRepo(db)
	}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	return s.auth(s.mux)
}

func (s *Server) routes() {
	s.mux.HandleFunc("POST /api/v1/sessions", s.handleCreateSession)
	s.mux.HandleFunc("GET /api/v1/sessions", s.handleListSessions)
	s.mux.HandleFunc("GET /api/v1/sessions/{id}", s.handleGetSession)
	s.mux.HandleFunc("GET /api/v1/sessions/{id}/memories", s.handleListMemories)
	s.mux.HandleFunc("DELETE /api/v1/sessions/{id}", s.handleDeleteSession)
	s.mux.HandleFunc("GET /api/v1/runs", s.handleListRuns)
	s.mux.HandleFunc("POST /api/v1/runs", s.handleCreateRun)
	s.mux.HandleFunc("GET /api/v1/runs/{id}", s.handleGetRun)
	s.mux.HandleFunc("GET /api/v1/runs/{id}/events", s.handleEvents)
	s.mux.HandleFunc("POST /api/v1/runs/{id}/cancel", s.handleCancel)
	s.mux.HandleFunc("POST /api/v1/runs/{id}/resume", s.handleResume)
	s.mux.HandleFunc("POST /api/v1/runs/{id}/approvals/{approval_id}", s.handleApproval)
	s.mux.HandleFunc("GET /api/v1/settings/tools", s.handleGetTools)
	s.mux.HandleFunc("PUT /api/v1/settings/tools", s.handleUpdateTools)
	s.mux.HandleFunc("GET /api/v1/runs/{id}/approvals", s.handleListApprovals)
	s.mux.HandleFunc("GET /api/v1/settings/model", s.handleGetModelSettings)
	s.mux.HandleFunc("PUT /api/v1/settings/model", s.handleUpdateModelSettings)
	s.mux.HandleFunc("GET /healthz", s.handleHealth)
	s.mux.HandleFunc("GET /readyz", s.handleReady)
	s.mux.Handle("/metrics", expvar.Handler())
}

func ownerOf(r *http.Request) string {
	if v := r.Header.Get("X-Owner-ID"); v != "" {
		return v
	}
	return "default"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string, retryable bool) {
	writeJSON(w, status, map[string]any{"code": code, "message": msg, "retryable": retryable})
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/healthz") || strings.HasPrefix(r.URL.Path, "/readyz") {
			next.ServeHTTP(w, r)
			return
		}
		if s.cfg.AuthToken != "" && r.Header.Get("Authorization") != "Bearer "+s.cfg.AuthToken {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "missing or invalid token", false)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if err := s.db.Ping(); err != nil {
		writeErr(w, 503, "not_ready", "db not ready", true)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ready"})
}

type createSessionReq struct {
	Title string `json:"title"`
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var in createSessionReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		writeErr(w, 400, "bad_request", "invalid body", false)
		return
	}
	id, err := s.sessions.Create(r.Context(), ownerOf(r), in.Title)
	if err != nil {
		writeErr(w, 500, "internal", "create session failed", true)
		return
	}
	writeJSON(w, 201, map[string]string{"session_id": id})
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	owner, title, ca, ua, err := s.tasks.GetSession(r.Context(), ownerOf(r), id)
	if err != nil {
		writeErr(w, 404, "not_found", "session not found", false)
		return
	}
	_ = owner
	writeJSON(w, 200, map[string]any{"session_id": id, "title": title, "created_at": ca, "updated_at": ua})
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.tasks.DeleteSession(r.Context(), ownerOf(r), id)
	if err != nil {
		if err == domain.ErrConflict {
			writeErr(w, 409, "conflict", "session has active runs; cancel them first", false)
			return
		}
		writeErr(w, 404, "not_found", "session not found", false)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.tasks.ListSessions(r.Context(), ownerOf(r))
	if err != nil {
		writeErr(w, 500, "internal", "list sessions failed", true)
		return
	}
	writeJSON(w, 200, map[string]any{"sessions": sessions})
}

func (s *Server) handleListMemories(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, _, _, _, err := s.tasks.GetSession(r.Context(), ownerOf(r), id); err != nil {
		writeErr(w, 404, "not_found", "session not found", false)
		return
	}
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	mems, err := s.memories.Latest(r.Context(), id, limit)
	if err != nil {
		writeErr(w, 500, "internal", "list memories failed", true)
		return
	}
	if mems == nil {
		mems = []domain.Memory{}
	}
	writeJSON(w, 200, map[string]any{"memories": mems})
}

func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	owner := ownerOf(r)
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		writeErr(w, 400, "bad_request", "session_id query param required", false)
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	runs, err := s.tasks.ListRunsBySession(r.Context(), owner, sessionID, limit)
	if err != nil {
		if err == domain.ErrForbidden || err == domain.ErrBadRequest {
			writeErr(w, 404, "not_found", "session not found", false)
			return
		}
		writeErr(w, 500, "internal", "list runs failed", true)
		return
	}
	writeJSON(w, 200, map[string]any{"runs": runs})
}

type createRunReq struct {
	SessionID       string        `json:"session_id"`
	Goal            string        `json:"goal"`
	Constraints     []string      `json:"constraints"`
	SuccessCriteria []string      `json:"success_criteria"`
	Mode            string        `json:"mode"`
	Budget          domain.Budget `json:"budget"`
}

func (s *Server) handleCreateRun(w http.ResponseWriter, r *http.Request) {
	var in createRunReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		writeErr(w, 400, "bad_request", "invalid body", false)
		return
	}
	run, dup, err := s.runs.CreateRun(r.Context(), service.CreateRunInput{
		OwnerID: ownerOf(r), SessionID: in.SessionID,
		Spec:   domain.TaskSpec{Goal: in.Goal, Constraints: in.Constraints, SuccessCriteria: in.SuccessCriteria, Mode: in.Mode},
		Budget: in.Budget, IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err == domain.ErrBadRequest {
		writeErr(w, 400, "bad_request", "invalid goal/session", false)
		return
	}
	if err != nil {
		writeErr(w, 500, "internal", "create run failed", true)
		return
	}
	writeJSON(w, 201, map[string]any{"run_id": run.ID, "status": run.Status, "duplicated": dup})
}

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run, err := s.tasks.GetRun(r.Context(), ownerOf(r), id)
	if err != nil {
		writeErr(w, 404, "not_found", "run not found", false)
		return
	}
	steps, err := s.tasks.ListSteps(r.Context(), ownerOf(r), id)
	if err != nil {
		writeErr(w, 500, "internal", "list steps failed", true)
		return
	}
	writeJSON(w, 200, map[string]any{"run": run, "steps": steps})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run, err := s.tasks.GetRun(r.Context(), ownerOf(r), id)
	if err != nil {
		writeErr(w, 404, "not_found", "run not found", false)
		return
	}
	after := parseAfter(r)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		evs, err := s.events.ListAfter(r.Context(), id, after, 500)
		if err != nil {
			writeErr(w, 500, "internal", "list events failed", true)
			return
		}
		for _, e := range evs {
			_, _ = w.Write([]byte("id: " + itoa64(e.Seq) + "\nevent: " + string(e.Type) + "\ndata: " + e.PayloadJSON + "\n\n"))
		}
		return
	}
	lastSeq := after
	writeEvents := func(evs []domain.AgentEvent) bool {
		for _, e := range evs {
			if _, err := w.Write([]byte("id: " + itoa64(e.Seq) + "\nevent: " + string(e.Type) + "\ndata: " + e.PayloadJSON + "\n\n")); err != nil {
				return false
			}
			if e.Seq > lastSeq {
				lastSeq = e.Seq
			}
		}
		flusher.Flush()
		return true
	}
	if evs, err := s.events.ListAfter(r.Context(), id, lastSeq, 500); err != nil {
		writeErr(w, 500, "internal", "list events failed", true)
		return
	} else if !writeEvents(evs) {
		return
	}
	if run.Status.Terminal() {
		return
	}
	ctx := r.Context()
	deadline := time.Now().Add(followMaxDuration)
	poll := time.NewTicker(followPollInterval)
	defer poll.Stop()
	beat := time.NewTicker(followHeartbeatEvery)
	defer beat.Stop()

	var pushCh <-chan domain.AgentEvent
	if s.hub != nil {
		var unsub func()
		pushCh, unsub = s.hub.Subscribe(id, 64)
		defer unsub()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-beat.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			flusher.Flush()
		case ev, ok := <-pushCh:
			if !ok {
				continue // defensive: unsub only via defer at handler exit; won't fire mid-loop
			}
			if ev.Seq > lastSeq {
				if ev.Seq > lastSeq+1 {
					// gap backfill：push 可能丢了中间 seq（订阅缓冲满 drop）。
					// 先 ListAfter(lastSeq) 把 gap 补回，再写 ev（幂等去重）。
					if gap, err := s.events.ListAfter(ctx, id, lastSeq, 500); err == nil {
						if !writeEvents(gap) {
							return
						}
					}
				}
				// ev 可能已包含在 gap 里 → 重复投递有意的、幂等（前端按 (seq,event_type) 去重）
				if !writeEvents([]domain.AgentEvent{ev}) {
					return
				}
			}
		case <-poll.C:
			if time.Now().After(deadline) {
				return
			}
			evs, err := s.events.ListAfter(ctx, id, lastSeq, 500)
			if err != nil {
				return
			}
			if !writeEvents(evs) {
				return
			}
			cur, err := s.tasks.GetRun(ctx, ownerOf(r), id)
			if err != nil {
				return
			}
			if cur.Status.Terminal() {
				return
			}
		}
	}
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.runs.Cancel(r.Context(), ownerOf(r), id); err != nil {
		msg := "cancel failed"
		if errors.Is(err, domain.ErrConflict) {
			msg = "run is not in a cancellable state"
		}
		writeErr(w, 409, "conflict", msg, false)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "cancelled"})
}

func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.runs.Resume(r.Context(), ownerOf(r), id); err != nil {
		msg := "resume failed"
		if errors.Is(err, domain.ErrConflict) {
			msg = "run is not in a resumable state"
		}
		writeErr(w, 409, "conflict", msg, false)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "running"})
}

type decideReq struct {
	Approve bool   `json:"approve"`
	Reason  string `json:"reason"`
}

func (s *Server) handleApproval(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	aid := r.PathValue("approval_id")
	var in decideReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		writeErr(w, 400, "bad_request", "invalid body", false)
		return
	}
	if err := s.runs.DecideApproval(r.Context(), service.DecideApprovalInput{
		OwnerID: ownerOf(r), RunID: id, ApprovalID: aid, Approve: in.Approve, Reason: in.Reason,
	}); err != nil {
		msg := "approval decision failed"
		if errors.Is(err, domain.ErrConflict) {
			msg = "approval already decided or expired"
		} else if errors.Is(err, domain.ErrForbidden) {
			msg = "forbidden"
		}
		writeErr(w, 409, "conflict", msg, false)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "decided"})
}

// handleListApprovals 只读列出某 run 的全部审批记录（bootstrap pending 卡用）。
func (s *Server) handleListApprovals(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	approvals, err := s.runs.ListApprovals(r.Context(), ownerOf(r), id)
	if err != nil {
		// GetRun 对不存在的 run 返回 sql.ErrNoRows（非 domain.ErrNotFound），
		// 对非本 owner 的 run 返回 domain.ErrForbidden；两者都走 404，避免泄露他人 run 是否存在。
		// ListByRun 的 DB 读失败仍走 500。
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, domain.ErrForbidden) {
			writeErr(w, 404, "not_found", "run not found", false)
			return
		}
		writeErr(w, 500, "internal", "list approvals failed", true)
		return
	}
	writeJSON(w, 200, map[string]any{"approvals": approvals})
}

func (s *Server) modelSettings(ctx context.Context) (provider, baseURL, name string, keySet bool) {
	provider, baseURL, name = s.cfg.ModelProvider, s.cfg.ModelBaseURL, s.cfg.ModelName
	keySet = s.cfg.ModelAPIKey != ""
	if v, ok, _ := s.settings.Get(ctx, "model.provider"); ok && v != "" {
		provider = v
	}
	if v, ok, _ := s.settings.Get(ctx, "model.base_url"); ok && v != "" {
		baseURL = v
	}
	if v, ok, _ := s.settings.Get(ctx, "model.name"); ok && v != "" {
		name = v
	}
	if _, ok, _ := s.settings.Get(ctx, "model.api_key"); ok {
		keySet = true
	}
	return provider, baseURL, name, keySet
}

func (s *Server) handleGetModelSettings(w http.ResponseWriter, r *http.Request) {
	provider, baseURL, name, keySet := s.modelSettings(r.Context())
	writeJSON(w, 200, map[string]any{
		"provider": provider, "base_url": baseURL, "model": name, "api_key_set": keySet,
	})
}

// handleGetTools returns the current tools status, or 501 if no provider was
// configured (a bare 7-arg New call).
func (s *Server) handleGetTools(w http.ResponseWriter, r *http.Request) {
	if s.tools == nil {
		writeErr(w, http.StatusNotImplemented, "tools_not_configured", "tools provider not set on this server", false)
		return
	}
	st, err := s.tools.GetToolsStatus(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "tools status failed", true)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// handleUpdateTools persists a tools config change and triggers reconcile via
// the provider (implemented by the App, Task 12). 501 if no provider.
func (s *Server) handleUpdateTools(w http.ResponseWriter, r *http.Request) {
	if s.tools == nil {
		writeErr(w, http.StatusNotImplemented, "tools_not_configured", "tools provider not set on this server", false)
		return
	}
	var in ToolConfigIn
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "invalid body", false)
		return
	}
	if err := s.tools.SaveTools(r.Context(), in); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "tools save failed", true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

type updateModelSettingsReq struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"base_url"`
	Model    string `json:"model"`
	APIKey   string `json:"api_key"`
}

func (s *Server) handleUpdateModelSettings(w http.ResponseWriter, r *http.Request) {
	var in updateModelSettingsReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		writeErr(w, 400, "bad_request", "invalid body", false)
		return
	}
	ctx := r.Context()
	if in.Provider != "" {
		if in.Provider != "openai_compat" {
			writeErr(w, 400, "bad_request", "unsupported provider", false)
			return
		}
		if err := s.settings.Set(ctx, "model.provider", in.Provider); err != nil {
			writeErr(w, 500, "internal", "save failed", true)
			return
		}
	}
	if in.BaseURL != "" {
		if err := s.settings.Set(ctx, "model.base_url", in.BaseURL); err != nil {
			writeErr(w, 500, "internal", "save failed", true)
			return
		}
	}
	if in.Model != "" {
		if err := s.settings.Set(ctx, "model.name", in.Model); err != nil {
			writeErr(w, 500, "internal", "save failed", true)
			return
		}
	}
	if in.APIKey != "" {
		if err := s.settings.Set(ctx, "model.api_key", in.APIKey); err != nil {
			writeErr(w, 500, "internal", "save failed", true)
			return
		}
	}
	provider, baseURL, name, keySet := s.modelSettings(ctx)
	writeJSON(w, 200, map[string]any{
		"provider": provider, "base_url": baseURL, "model": name, "api_key_set": keySet,
	})
}

func parseAfter(r *http.Request) int64 {
	if v := r.URL.Query().Get("after"); v != "" {
		var n int64
		for _, c := range v {
			if c < '0' || c > '9' {
				n = 0
				break
			}
			n = n*10 + int64(c-'0')
		}
		return n
	}
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		var n int64
		for _, c := range v {
			if c < '0' || c > '9' {
				return 0
			}
			n = n*10 + int64(c-'0')
		}
		return n
	}
	return 0
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	b := []byte{}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
