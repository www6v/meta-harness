package harness

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

var testUpgrader = websocket.Upgrader{}

// fakeDshGateway answers dsh-web-style RPC calls and serves WS events
// using the Typert RPC protocol (/api/remote.mux).
func fakeDshGateway(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/api/", func(
		w http.ResponseWriter, r *http.Request,
	) {
		var body dshClientRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		// Handle different RPC methods
		switch body.Method {
		case "session/create":
			payload := body.Payload.(map[string]any)
			args := payload["args"].(map[string]any)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"type":  "server-response",
				"rpcId": body.RpcID,
				"result": map[string]any{
					"ok": true, "value": map[string]any{"sessionId": args["sessionId"]},
				},
			})
		case "session/prompt":
			payload := body.Payload.(map[string]any)
			args := payload["args"].(map[string]any)
			request := args["request"].(map[string]any)
			if request["sessionId"] == "" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"type":  "server-response",
					"rpcId": body.RpcID,
					"result": map[string]any{
						"ok": false,
						"error": map[string]any{
							"code": "session-not-found", "message": "sessionId required",
						},
					},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"type":  "server-response",
				"rpcId": body.RpcID,
				"result": map[string]any{
					"ok": true, "value": map[string]any{"accepted": true},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"type":  "server-response",
				"rpcId": body.RpcID,
				"result": map[string]any{
					"ok": false,
					"error": map[string]any{
						"code": "not-found", "message": "method not found: " + body.Method,
					},
				},
			})
		}
	})

	// Use the correct Typert RPC streaming endpoint
	mux.HandleFunc("/api/remote.mux", func(
		w http.ResponseWriter, r *http.Request,
	) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Read the open message from client
		var openMsg struct {
			Type     string `json:"type"`
			StreamID string `json:"streamId"`
			Endpoint string `json:"endpoint"`
		}
		if err := conn.ReadJSON(&openMsg); err != nil {
			return
		}
		if openMsg.Type != "open" {
			return
		}
		streamID := openMsg.StreamID

		// Send item messages with session events using the new protocol format
		frames := []map[string]any{
			// assistant/message with usage
			{
				"type":     "item",
				"streamId": streamID,
				"value": map[string]any{
					"type": "assistant/message",
					"seq":  1,
					"data": map[string]any{
						"message": map[string]any{
							"content": []map[string]any{
								{"type": "text", "text": "hello from dsh"},
							},
						},
						"usage": map[string]any{
							"inputTokens":  10,
							"outputTokens": 5,
						},
					},
				},
			},
			// turn/end
			{
				"type":     "item",
				"streamId": streamID,
				"value": map[string]any{
					"type": "turn/end",
					"seq":  2,
					"data": map[string]any{
						"reason": map[string]any{"kind": "completed"},
					},
				},
			},
		}
		for _, f := range frames {
			_ = conn.WriteJSON(f)
		}
		// Send end message
		_ = conn.WriteJSON(map[string]any{
			"type":     "end",
			"streamId": streamID,
		})
		// Hold until client disconnects.
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})

	return httptest.NewServer(mux)
}

func TestDeepSeekClient_RunTurn(t *testing.T) {
	srv := fakeDshGateway(t)
	defer srv.Close()

	c := &DeepSeekClient{GatewayURL: srv.URL}
	resp, err := c.RunTurn(context.Background(), TurnRequest{
		SessionID: "sess-1",
		Events: []json.RawMessage{json.RawMessage(
			`{"type":"user.message","content":[{"type":"text","text":"hi"}]}`)},
	})
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if len(resp.Events) == 0 {
		t.Fatalf("expected at least one mapped event")
	}
	var msg struct {
		Type    string `json:"type"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	_ = json.Unmarshal(resp.Events[0], &msg)
	if msg.Type != "agent.message" {
		t.Fatalf("first event type = %q, want agent.message", msg.Type)
	}
	if msg.Content[0].Text != "hello from dsh" {
		t.Fatalf("text = %q", msg.Content[0].Text)
	}
	if resp.Usage == nil || resp.Usage.InputTokens != 10 ||
		resp.Usage.OutputTokens != 5 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestDeepSeekClient_RunTurn_RpcError(t *testing.T) {
	srv := fakeDshGateway(t)
	defer srv.Close()

	c := &DeepSeekClient{GatewayURL: srv.URL}
	_, err := c.RunTurn(context.Background(), TurnRequest{
		SessionID: "", // forces input-invalid from the fake gateway
		Events:    []json.RawMessage{},
	})
	if err == nil || !strings.Contains(err.Error(), "session-not-found") {
		t.Fatalf("expected rpc error surfaced, got %v", err)
	}
}

func TestDeepSeekClient_RunTurn_GatewayDown(t *testing.T) {
	c := &DeepSeekClient{GatewayURL: "http://127.0.0.1:1"}
	_, err := c.RunTurn(context.Background(), TurnRequest{SessionID: "s"})
	if err == nil {
		t.Fatalf("expected connection error")
	}
}

func TestDeepSeekClient_RunTurnStream(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", func(
		w http.ResponseWriter, r *http.Request,
	) {
		var body dshClientRequest
		_ = json.NewDecoder(r.Body).Decode(&body)

		switch body.Method {
		case "session/create":
			payload := body.Payload.(map[string]any)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"type":  "server-response",
				"rpcId": body.RpcID,
				"result": map[string]any{"ok": true, "value": nil},
			})
			_ = payload // avoid unused variable error
		case "session/prompt":
			payload := body.Payload.(map[string]any)
			args := payload["args"].(map[string]any)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"type":  "server-response",
				"rpcId": body.RpcID,
				"result": map[string]any{"ok": true, "value": map[string]any{"accepted": true}},
			})
			_ = args // use args to avoid unused variable error
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	// Use the correct Typert RPC streaming endpoint
	mux.HandleFunc("/api/remote.mux", func(
		w http.ResponseWriter, r *http.Request,
	) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Read the open message from client
		var openMsg struct {
			Type     string `json:"type"`
			StreamID string `json:"streamId"`
			Endpoint string `json:"endpoint"`
		}
		if err := conn.ReadJSON(&openMsg); err != nil {
			return
		}
		if openMsg.Type != "open" {
			return
		}
		streamID := openMsg.StreamID

		// Send item messages with session events using the new protocol format
		frames := []map[string]any{
			// chunk "hel"
			{
				"type":     "item",
				"streamId": streamID,
				"value": map[string]any{
					"type": "assistant/chunk",
					"seq":  1,
					"data": map[string]any{
						"chunk": map[string]any{"type": "text-delta", "text": "hel"},
					},
				},
			},
			// chunk "lo"
			{
				"type":     "item",
				"streamId": streamID,
				"value": map[string]any{
					"type": "assistant/chunk",
					"seq":  2,
					"data": map[string]any{
						"chunk": map[string]any{"type": "text-delta", "text": "lo"},
					},
				},
			},
			// assistant/message
			{
				"type":     "item",
				"streamId": streamID,
				"value": map[string]any{
					"type": "assistant/message",
					"seq":  3,
					"data": map[string]any{
						"message": map[string]any{
							"content": []map[string]any{
								{"type": "text", "text": "hello"},
							},
						},
						"usage": map[string]any{
							"inputTokens":  3,
							"outputTokens": 2,
						},
					},
				},
			},
			// turn/end
			{
				"type":     "item",
				"streamId": streamID,
				"value": map[string]any{
					"type": "turn/end",
					"seq":  4,
					"data": map[string]any{
						"reason": map[string]any{"kind": "completed"},
					},
				},
			},
		}
		for _, f := range frames {
			_ = conn.WriteJSON(f)
		}
		// Send end message
		_ = conn.WriteJSON(map[string]any{
			"type":     "end",
			"streamId": streamID,
		})
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &DeepSeekClient{GatewayURL: srv.URL}
	var types []string
	err := c.RunTurnStream(context.Background(), TurnRequest{
		SessionID: "sess-9",
		Events: []json.RawMessage{json.RawMessage(
			`{"type":"user.message","content":[{"type":"text","text":"hi"}]}`)},
	}, func(ev json.RawMessage) error {
		var meta struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(ev, &meta)
		types = append(types, meta.Type)
		return nil
	})
	if err != nil {
		t.Fatalf("RunTurnStream: %v", err)
	}
	// We expect: 2 chunk events (hel, hello) + 1 assistant/message = 3
	if len(types) < 2 {
		t.Fatalf("events = %v (len=%d), want >= 2", types, len(types))
	}
	for _, tp := range types {
		if tp != "agent.message" {
			t.Errorf("unexpected event type %q", tp)
		}
	}
}
