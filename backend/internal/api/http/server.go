package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"expvar"
	"net/http"
	"strconv"
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
	s.mux.HandleFunc("GET /api/v1/sessions", s.handleListSessions)
	s.mux.HandleFunc("GET /api/v1/sessions/{id}", s.handleGetSession)
	s.mux.HandleFunc("GET /api/v1/runs", s.handleListRuns)
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

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.tasks.ListSessions(r.Context(), ownerOf(r))
	if err != nil {
		writeErr(w, 500, "internal", "list sessions failed", true)
		return
	}
	writeJSON(w, 200, map[string]any{"sessions": sessions})
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
	if _, err := s.tasks.GetRun(r.Context(), ownerOf(r), id); err != nil {
		writeErr(w, 404, "not_found", "run not found", false)
		return
	}
	after := parseAfter(r)
	evs, err := s.events.ListAfter(r.Context(), id, after, 500)
	if err != nil {
		writeErr(w, 500, "internal", "list events failed", true)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)
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
