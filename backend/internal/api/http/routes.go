package httpapi

import (
	"expvar"
	"net/http/pprof"
)

func (s *Server) routes() {
	s.mux.HandleFunc("POST /api/v1/sessions", s.handleCreateSession)
	s.mux.HandleFunc("GET /api/v1/sessions", s.handleListSessions)
	s.mux.HandleFunc("GET /api/v1/sessions/{id}", s.handleGetSession)
	s.mux.HandleFunc("GET /api/v1/sessions/{id}/memories", s.handleListMemories)
	s.mux.HandleFunc("DELETE /api/v1/sessions/{id}", s.handleDeleteSession)
	s.mux.HandleFunc("PATCH /api/v1/sessions/{id}", s.handleRenameSession)
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
	s.mux.HandleFunc("GET /version", s.handleVersion)
	s.mux.Handle("/metrics", expvar.Handler())
	s.mux.HandleFunc("GET /metrics/prometheus", s.handlePromMetrics)
	if s.pprofEnabled {
		s.mux.HandleFunc("GET /debug/pprof/", pprof.Index)
		s.mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
		s.mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
		s.mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
		s.mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
	}
}
