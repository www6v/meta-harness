package api

import (
	"strings"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/open-ma/oma-building/internal/harness"
)

// mockCodexBridge creates a test HTTP server that mimics the Python bridge.
func mockCodexBridge(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/codex/rpc" {
			http.NotFound(w, r)
			return
		}

		var req struct {
			SessionID string `json:"session_id"`
			Method    string `json:"method"`
			Params    map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		switch req.Method {
		case "mcpServerStatus/list":
			json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{
					"servers": []map[string]any{
						{"name": "github", "status": "connected"},
						{"name": "linear", "status": "connected"},
					},
				},
			})
		case "config/mcpServer/reload":
			json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{
					"success": true,
				},
			})
		case "thread/list":
			json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{
					"threads": []map[string]any{
						{"id": "thread-001", "name": "Test Thread", "status": "active"},
					},
				},
			})
		case "thread/read":
			threadID, _ := req.Params["thread_id"].(string)
			json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{
					"thread": map[string]any{
						"id":     threadID,
						"name":   "Test Thread",
						"status": "active",
					},
				},
			})
		default:
			json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{
					"code":    -32601,
					"message": "Method not found",
				},
			})
		}
	}))
}

func TestCodexMCPStatus(t *testing.T) {
	bridge := mockCodexBridge(t)
	defer bridge.Close()

	client := &harness.CodexClient{BridgeURL: bridge.URL}
	r := chi.NewRouter()
	mountCodexMCPRoutes(r, codexMCPDeps{CodexClient: client})

	req := httptest.NewRequest("GET", "/v1/codex/mcp/status?session_id=test-session", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	servers, ok := resp["servers"].([]any)
	if !ok || len(servers) != 2 {
		t.Fatalf("expected 2 servers, got %v", resp)
	}
}

func TestCodexMCPReload(t *testing.T) {
	bridge := mockCodexBridge(t)
	defer bridge.Close()

	client := &harness.CodexClient{BridgeURL: bridge.URL}
	r := chi.NewRouter()
	mountCodexMCPRoutes(r, codexMCPDeps{CodexClient: client})

	body := `{"session_id":"test-session"}`
	req := httptest.NewRequest("POST", "/v1/codex/mcp/reload", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if resp["success"] != true {
		t.Fatalf("expected success=true, got %v", resp)
	}
}

func TestCodexThreadsList(t *testing.T) {
	bridge := mockCodexBridge(t)
	defer bridge.Close()

	client := &harness.CodexClient{BridgeURL: bridge.URL}
	r := chi.NewRouter()
	mountCodexMCPRoutes(r, codexMCPDeps{CodexClient: client})

	req := httptest.NewRequest("GET", "/v1/codex/threads?session_id=test-session", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	threads, ok := resp["threads"].([]any)
	if !ok || len(threads) != 1 {
		t.Fatalf("expected 1 thread, got %v", resp)
	}
}

func TestCodexThreadRead(t *testing.T) {
	bridge := mockCodexBridge(t)
	defer bridge.Close()

	client := &harness.CodexClient{BridgeURL: bridge.URL}
	r := chi.NewRouter()
	mountCodexMCPRoutes(r, codexMCPDeps{CodexClient: client})

	req := httptest.NewRequest("GET", "/v1/codex/threads/thread-001?session_id=test-session", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	thread, ok := resp["thread"].(map[string]any)
	if !ok {
		t.Fatalf("expected thread object, got %v", resp)
	}
	if thread["id"] != "thread-001" {
		t.Fatalf("expected thread-001, got %v", thread["id"])
	}
}
