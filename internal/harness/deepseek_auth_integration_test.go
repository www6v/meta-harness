package harness

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeepSeekClient_WithAuth(t *testing.T) {
	// Create a test server that checks for auth cookie
	var receivedCookie string
	var receivedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedCookie = r.Header.Get("Cookie")
		receivedPath = r.URL.Path
		t.Logf("Received request: path=%s, Cookie=%s", receivedPath, receivedCookie)

		// Return a valid RPC response
		var body dshClientRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Logf("Failed to decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// Extract args from payload
		payload := body.Payload.(map[string]any)
		args := payload["args"].(map[string]any)

		_ = json.NewEncoder(w).Encode(map[string]any{
			"type":  "server-response",
			"rpcId": body.RpcID,
			"result": map[string]any{
				"ok":    true,
				"value": map[string]any{"sessionId": args["sessionId"]},
			},
		})
	}))
	defer srv.Close()

	// Create a client pointing to the test server
	c := &DeepSeekClient{GatewayURL: srv.URL}

	// Try to load the auth cookie (will fail if credentials file doesn't exist)
	cookie := c.getAuthCookie()
	if cookie != nil {
		t.Logf("Auth cookie loaded: name=%s, value_length=%d", cookie.Name, len(cookie.Value))
	} else {
		t.Log("Auth cookie not loaded (credentials file may not exist)")
	}

	// Make a test RPC call
	err := c.rpc(context.Background(), "session.create", map[string]any{
		"sessionId": "test-session",
	}, nil)

	if err != nil {
		t.Fatalf("RPC call failed: %v", err)
	}

	// Check if cookie was sent
	if receivedCookie != "" {
		t.Logf("Cookie was sent in request: %s", receivedCookie)
	} else {
		t.Log("No cookie was sent in request")
	}
}
