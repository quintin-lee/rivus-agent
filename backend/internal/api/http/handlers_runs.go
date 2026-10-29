package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/service"
	"rivus-agent-backend/internal/store"
)

func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	owner := ownerOf(r)
	sessionID := r.URL.Query().Get("session_id")
	status := r.URL.Query().Get("status")
	if status != "" && !domain.RunStatus(status).Valid() {
		writeErr(w, 400, "bad_request", "invalid status", false)
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	offset := 0
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			offset = n
		}
	}
	cursor := r.URL.Query().Get("cursor")
	if cursor != "" {
		if _, _, err := store.DecodeCursor(cursor); err != nil {
			writeErr(w, 400, "bad_request", "invalid cursor", false)
			return
		}
	}
	// session_id 缺省 = 跨会话历史（仅顶层 run）；存在 = 会话内列表（保持原行为）
	if sessionID == "" {
		runs, err := s.tasks.ListRunsByOwner(r.Context(), owner, status, r.URL.Query().Get("q"), limit, offset, cursor)
		if err != nil {
			writeErr(w, 500, "internal", "list runs failed", true)
			return
		}
		resp := map[string]any{"runs": runs}
		if len(runs) == limit {
			last := runs[len(runs)-1]
			resp["next_cursor"] = store.EncodeCursor(last.CreatedAt, last.ID)
		}
		writeJSON(w, 200, resp)
		return
	}
	runs, err := s.tasks.ListRunsBySession(r.Context(), owner, sessionID, status, r.URL.Query().Get("q"), limit, offset, cursor)
	if err != nil {
		if err == domain.ErrForbidden || err == domain.ErrBadRequest {
			writeErr(w, 404, "not_found", "session not found", false)
			return
		}
		writeErr(w, 500, "internal", "list runs failed", true)
		return
	}
	resp := map[string]any{"runs": runs}
	if len(runs) == limit {
		last := runs[len(runs)-1]
		resp["next_cursor"] = store.EncodeCursor(last.CreatedAt, last.ID)
	}
	writeJSON(w, 200, resp)
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
