package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/service"
)

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

type renameSessionReq struct {
	Title string `json:"title"`
}

// handleRenameSession 重命名会话：空 title → 400；不存在/非属主 → 404；成功回读返回会话全量。
func (s *Server) handleRenameSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in renameSessionReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		writeErr(w, 400, "bad_request", "invalid body", false)
		return
	}
	if err := s.sessions.Rename(r.Context(), ownerOf(r), id, in.Title); err != nil {
		if errors.Is(err, service.ErrEmptyTitle) {
			writeErr(w, 400, "bad_request", "title is empty", false)
			return
		}
		writeErr(w, 404, "not_found", "session not found", false)
		return
	}
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
	offset := 0
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			offset = n
		}
	}
	sessions, err := s.tasks.ListSessions(r.Context(), ownerOf(r), r.URL.Query().Get("q"), 50, offset)
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
