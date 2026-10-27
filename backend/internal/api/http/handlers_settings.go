package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
)

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
