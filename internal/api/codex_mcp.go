package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/open-ma/oma-building/internal/harness"
)

type codexMCPDeps struct {
	CodexClient *harness.CodexClient
}

func mountCodexMCPRoutes(r chi.Router, deps codexMCPDeps) {
	if deps.CodexClient == nil {
		return
	}
	h := &codexMCPHandler{deps: deps}
	r.Route("/v1/codex", func(r chi.Router) {
		r.Get("/mcp/status", h.getMCPStatus)
		r.Post("/mcp/reload", h.reloadMCP)
		r.Get("/threads", h.listThreads)
		r.Get("/threads/{id}", h.getThread)
	})
}

type codexMCPHandler struct {
	deps codexMCPDeps
}

// GET /v1/codex/mcp/status?session_id=xxx
func (h *codexMCPHandler) getMCPStatus(w http.ResponseWriter, r *http.Request) {
	sid := r.URL.Query().Get("session_id")
	if sid == "" {
		writeError(w, http.StatusBadRequest, "session_id required")
		return
	}

	status, err := h.deps.CodexClient.ListMCPServerStatus(r.Context(), sid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"servers": status,
	})
}

// POST /v1/codex/mcp/reload
func (h *codexMCPHandler) reloadMCP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.SessionID == "" {
		writeError(w, http.StatusBadRequest, "session_id required")
		return
	}

	if err := h.deps.CodexClient.ReloadMCPServers(r.Context(), req.SessionID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"success": true,
	})
}

// GET /v1/codex/threads?session_id=xxx
func (h *codexMCPHandler) listThreads(w http.ResponseWriter, r *http.Request) {
	sid := r.URL.Query().Get("session_id")
	if sid == "" {
		writeError(w, http.StatusBadRequest, "session_id required")
		return
	}

	threads, err := h.deps.CodexClient.ListThreads(r.Context(), sid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"threads": threads,
	})
}

// GET /v1/codex/threads/{id}?session_id=xxx
func (h *codexMCPHandler) getThread(w http.ResponseWriter, r *http.Request) {
	tid := chi.URLParam(r, "id")
	sid := r.URL.Query().Get("session_id")
	if tid == "" || sid == "" {
		writeError(w, http.StatusBadRequest, "thread_id and session_id required")
		return
	}

	thread, err := h.deps.CodexClient.ReadThread(r.Context(), sid, tid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"thread": thread,
	})
}
