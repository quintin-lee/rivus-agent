package httpapi

import (
	"database/sql"
	"encoding/json"
	"expvar"
	"net/http"
	"strings"

	"rivus-agent-backend/internal/config"
	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/service"
	"rivus-agent-backend/internal/store"
)

type Server struct {
	cfg      config.Config
	db       *sql.DB
	tasks    *store.TaskRepo
	events   *store.EventRepo
	runs     *service.RunService
	sessions *service.SessionService
	mux      *http.ServeMux
}

func New(cfg config.Config, db *sql.DB, tasks *store.TaskRepo, events *store.EventRepo, runs *service.RunService, sessions *service.SessionService) *Server {
	s := &Server{cfg: cfg, db: db, tasks: tasks, events: events, runs: runs, sessions: sessions, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	return s.auth(s.mux)
}

func (s *Server) routes() {
	s.mux.HandleFunc("POST /api/v1/sessions", s.handleCreateSession)
	s.mux.HandleFunc("GET /api/v1/sessions/{id}", s.handleGetSession)
	s.mux.HandleFunc("POST /api/v1/runs", s.handleCreateRun)
	s.mux.HandleFunc("GET /api/v1/runs/{id}", s.handleGetRun)
	s.mux.HandleFunc("GET /api/v1/runs/{id}/events", s.handleEvents)
	s.mux.HandleFunc("POST /api/v1/runs/{id}/cancel", s.handleCancel)
	s.mux.HandleFunc("POST /api/v1/runs/{id}/resume", s.handleResume)
	s.mux.HandleFunc("POST /api/v1/runs/{id}/approvals/{approval_id}", s.handleApproval)
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
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in)
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
	steps, _ := s.tasks.ListSteps(r.Context(), ownerOf(r), id)
	writeJSON(w, 200, map[string]any{"run": run, "steps": steps})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.tasks.GetRun(r.Context(), ownerOf(r), id); err != nil {
		writeErr(w, 404, "not_found", "run not found", false)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)
	var after int64
	_, _ = s.events, after
	evs, err := s.events.ListAfter(r.Context(), id, 0, 500)
	if err != nil {
		return
	}
	for _, e := range evs {
		_, _ = w.Write([]byte("id: " + itoa64(e.Seq) + "\nevent: " + string(e.Type) + "\ndata: " + e.PayloadJSON + "\n\n"))
	}
	if flusher != nil {
		flusher.Flush()
	}
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.runs.Cancel(r.Context(), ownerOf(r), id); err != nil {
		writeErr(w, 409, "conflict", err.Error(), false)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "cancelled"})
}

func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.runs.Resume(r.Context(), ownerOf(r), id); err != nil {
		writeErr(w, 409, "conflict", err.Error(), false)
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
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in)
	if err := s.runs.DecideApproval(r.Context(), service.DecideApprovalInput{
		OwnerID: ownerOf(r), RunID: id, ApprovalID: aid, Approve: in.Approve, Reason: in.Reason,
	}); err != nil {
		writeErr(w, 409, "conflict", err.Error(), false)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "decided"})
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
