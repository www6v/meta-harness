package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// DeepSeekClient calls the DeepSeek harness (dsh) web gateway using the
// Typert RPC protocol:
// - Unary calls: POST /api/<method> with ClientRequest/ServerResponse envelopes
// - Streaming: WebSocket /api/remote.mux with open/item/end message types
type DeepSeekClient struct {
	// GatewayURL is the dsh web base URL, e.g. "http://dsh:3080".
	GatewayURL string
	// Token is an optional bearer token (dsh ships without auth).
	Token string
	// HTTP overrides the transport; nil uses a 10-minute timeout client.
	HTTP *http.Client
	// authCookie caches the dsh browser-auth cookie; lazily initialized.
	authCookie *dshAuthCookie
}

// dshClientRequest is the uplink wire envelope (Typert RPC ClientRequest).
type dshClientRequest struct {
	Type    string `json:"type"`
	RpcID   string `json:"rpcId"`
	Method  string `json:"method"`
	Payload any    `json:"payload"`
}

// dshServerResponse is the response envelope. Business errors arrive
// as HTTP 200 with result.ok=false; HTTP status codes are carrier-only.
type dshServerResponse struct {
	Type   string `json:"type"`
	RpcID  string `json:"rpcId"`
	Result struct {
		OK    bool            `json:"ok"`
		Value json.RawMessage `json:"value,omitempty"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	} `json:"result"`
}

// rpc posts one dotted-method call to POST /api/<method>.
// The method name is sent both in the URL path and the request body.
func (c *DeepSeekClient) rpc(
	ctx context.Context,
	method string,
	payload map[string]any,
	out any,
) error {
	// Convert method name from "session.create" to "session/create" format
	rpcMethod := strings.ReplaceAll(method, ".", "/")

	body, err := json.Marshal(dshClientRequest{
		Type:    "client-request",
		RpcID:   randomOCID(),
		Method:  rpcMethod,
		Payload: map[string]any{"args": payload}, // payload 必须包装在 args 中
	})
	if err != nil {
		return err
	}
	// Trim trailing slash from GatewayURL to avoid double slashes in the path
	baseURL := strings.TrimRight(c.GatewayURL, "/")
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost,
		baseURL+"/api/"+rpcMethod,
		bytes.NewReader(body),
	)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	// Add dsh browser-auth cookie if available
	if cookie := c.getAuthCookie(); cookie != nil {
		req.Header.Set("Cookie", cookie.Name+"="+cookie.Value)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		log.Printf("[DSH RPC ERROR] method=%s status=%d body=%s", rpcMethod, resp.StatusCode, strings.TrimSpace(string(raw)))
		return fmt.Errorf("deepseek rpc %s status=%d: %s",
			rpcMethod, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var env dshServerResponse
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return fmt.Errorf("deepseek rpc %s decode: %w", method, err)
	}
	if !env.Result.OK {
		msg := "unknown error"
		if env.Result.Error != nil {
			msg = env.Result.Error.Code + ": " + env.Result.Error.Message
		}
		return fmt.Errorf("deepseek rpc %s: %s", method, msg)
	}
	if out != nil {
		return json.Unmarshal(env.Result.Value, out)
	}
	return nil
}

func (c *DeepSeekClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 10 * time.Minute}
}

// getAuthCookie returns the cached dsh auth cookie, initializing it if needed.
// Returns nil if the cookie cannot be loaded (e.g., credentials file missing).
func (c *DeepSeekClient) getAuthCookie() *dshAuthCookie {
	if c.authCookie != nil {
		return c.authCookie
	}
	authority := extractAuthority(c.GatewayURL)
	log.Printf("[DSH CLIENT] loading auth cookie for authority=%s", authority)
	cookie, err := loadDshAuthCookie(authority)
	if err != nil {
		// Log the error but don't fail - the request will proceed without auth
		// and may fail with 401 if the server requires it
		log.Printf("[DSH CLIENT] failed to load auth cookie: %v", err)
		return nil
	}
	log.Printf("[DSH CLIENT] auth cookie loaded successfully")
	c.authCookie = cookie
	return cookie
}

// ensureSession creates the dsh session on first use. session-conflict
// means it already exists — not an error.
func (c *DeepSeekClient) ensureSession(
	ctx context.Context, sessionID string,
) error {
	// session.create expects the request object wrapped in "request" field
	// because the @Remote('create') decorator expects: create(request: SessionCreateRequest)
	// cwd must be an absolute path (deepseek-harness requirement)
	cwd, _ := os.Getwd()
	if cwd == "" {
		cwd = "."
	}
	// Create a unique cwd per session for isolation
	// This ensures each oma-session has its own DSH file namespace
	sessionCwd := filepath.Join(cwd, "dsh-sessions", sessionID)
	if err := os.MkdirAll(sessionCwd, 0o755); err != nil {
		// If we can't create the session dir, fall back to base cwd
		sessionCwd = cwd
	}
	err := c.rpc(ctx, "session.create", map[string]any{
		"request": map[string]any{
			"sessionId": sessionID,
			"cwd":       sessionCwd,
		},
	}, nil)
	if err != nil && strings.Contains(err.Error(), "session-conflict") {
		return nil
	}
	return err
}

// CreateSession creates a new deepseek-harness session and returns the DSH session UUID.
// The sessionID is the oma-session identifier used as the cwd-based session name.
// Returns the DSH session UUID which should be stored in the oma-session's dsh_session_id field.
func (c *DeepSeekClient) CreateSession(
	ctx context.Context, sessionID string,
) (string, error) {
	// Each DSH session needs a unique cwd for file isolation.
	// We use a session-specific subdirectory to ensure isolation.
	// DSH will create the session under ~/.dsh/sessions/{sessionId}/
	// The workspaceFileScopeId for RPC calls should be this sessionId.

	cwd, _ := os.Getwd()
	if cwd == "" {
		cwd = "."
	}
	// Create a unique cwd per session for isolation
	// This ensures each oma-session has its own DSH file namespace
	sessionCwd := filepath.Join(cwd, "dsh-sessions", sessionID)
	if err := os.MkdirAll(sessionCwd, 0o755); err != nil {
		// If we can't create the session dir, fall back to base cwd
		sessionCwd = cwd
	}

	// Create the session - if it already exists, that's fine
	var result map[string]any
	err := c.rpc(ctx, "session.create", map[string]any{
		"request": map[string]any{
			"sessionId": sessionID,
			"cwd":       sessionCwd,
		},
	}, &result)
	if err != nil && strings.Contains(err.Error(), "session-conflict") {
		// Session already exists, return the sessionID as the scope ID
		return sessionID, nil
	}
	if err != nil {
		return "", err
	}

	// Return the oma-session ID as the workspaceFileScopeId
	// DSH uses this to isolate the session's workspace files
	return sessionID, nil
}

// DeleteSession deletes a deepseek-harness session.
func (c *DeepSeekClient) DeleteSession(
	ctx context.Context, sessionID string,
) error {
	// DSH doesn't have a direct session.delete RPC.
	// The session will be cleaned up by DSH's garbage collection.
	// For now, we just ensure the session is not in use.
	_ = sessionID
	return nil
}

// RunTurn implements Client. It creates the dsh session, prompts, and
// collects events via WebSocket until turn/end.
func (c *DeepSeekClient) RunTurn(
	ctx context.Context,
	req TurnRequest,
) (TurnResponse, error) {
	start := time.Now()
	userText := extractLastUserMessage(req.Events)
	if userText == "" {
		userText = "(continue)"
	}

	if err := c.ensureSession(ctx, req.SessionID); err != nil {
		logTurn("backend", "deepseek", "session", req.SessionID,
			"duration_ms", time.Since(start).Milliseconds(), "error", err)
		return TurnResponse{}, err
	}

	if err := c.rpc(ctx, "session.prompt", map[string]any{
		"request": map[string]any{
			"requestId": randomOCID(),
			"sessionId": req.SessionID,
			"mode":      "queue",
			"content":   []map[string]any{{"type": "text", "text": userText}},
		},
	}, nil); err != nil {
		logTurn("backend", "deepseek", "session", req.SessionID,
			"duration_ms", time.Since(start).Milliseconds(), "error", err)
		return TurnResponse{}, err
	}

	events, usage, err := c.collectTurn(ctx, req.SessionID)
	if err != nil {
		logTurn("backend", "deepseek", "session", req.SessionID,
			"duration_ms", time.Since(start).Milliseconds(), "error", err)
		return TurnResponse{}, err
	}
	logTurn("backend", "deepseek", "session", req.SessionID,
		"duration_ms", time.Since(start).Milliseconds())
	return TurnResponse{Events: events, Usage: usage}, nil
}

// remoteMuxOpenMessage is sent to open a logical stream on /api/remote.mux.
type remoteMuxOpenMessage struct {
	Type     string `json:"type"`
	StreamID string `json:"streamId"`
	Endpoint string `json:"endpoint"`
	Payload  any    `json:"payload"`
}

// remoteMuxItemMessage is received for each stream item.
type remoteMuxItemMessage struct {
	Type     string          `json:"type"`
	StreamID string          `json:"streamId"`
	Value    json.RawMessage `json:"value,omitempty"`
}

// remoteMuxEndMessage signals the end of a stream.
type remoteMuxEndMessage struct {
	Type     string `json:"type"`
	StreamID string `json:"streamId"`
}

// remoteMuxErrorMessage signals a stream error.
type remoteMuxErrorMessage struct {
	Type     string `json:"type"`
	StreamID string `json:"streamId"`
	Error    struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// sessionFollowFrame is the frame structure returned by session/follow stream
// Format: { type: "event", event: { type, seq, data } } or { type: "assistant-stream", frame: ... }
type sessionFollowFrame struct {
	Type  string          `json:"type"`
	Event json.RawMessage `json:"event"`
}

// dshSessionEvent is the dsh SessionEvent envelope
// (packages/core/session/src/types.ts): { type, seq, time, data }.
type dshSessionEvent struct {
	Type string          `json:"type"`
	Seq  int             `json:"seq"`
	Data json.RawMessage `json:"data"`
}

// collectTurn dials the WS mux, opens a session/follow stream, collects
// events until turn/end, and returns mapped oma events plus token usage.
func (c *DeepSeekClient) collectTurn(
	ctx context.Context,
	sessionID string,
) ([]json.RawMessage, *TurnUsage, error) {
	// Use the correct Typert RPC streaming endpoint
	baseURL := strings.TrimRight(c.GatewayURL, "/")
	wsURL := strings.Replace(baseURL, "http", "ws", 1) + "/api/remote.mux"
	header := http.Header{}
	if c.Token != "" {
		header.Set("Authorization", "Bearer "+c.Token)
	}
	// Add dsh browser-auth cookie if available
	if cookie := c.getAuthCookie(); cookie != nil {
		header.Set("Cookie", cookie.Name+"="+cookie.Value)
	}
	dialer := websocket.DefaultDialer
	conn, _, err := dialer.DialContext(ctx, wsURL, header)
	if err != nil {
		return nil, nil, fmt.Errorf("deepseek remote.mux dial: %w", err)
	}
	defer conn.Close()

	streamID := randomOCID()

	// Open the session/follow stream
	// session/follow expects: Payload.args.request.address (SessionFollowRequest)
	// address must be { kind: "session", sessionId: "..." } format
	openMsg := remoteMuxOpenMessage{
		Type:     "open",
		StreamID: streamID,
		Endpoint: "session/follow",
		Payload: map[string]any{
			"args": map[string]any{
				"request": map[string]any{
					"address": map[string]any{
						"kind":      "session",
						"sessionId": sessionID,
					},
				},
			},
		},
	}
	if err := conn.WriteJSON(openMsg); err != nil {
		return nil, nil, fmt.Errorf("remote.mux open: %w", err)
	}

	var events []json.RawMessage
	var usage *TurnUsage
	var accumulated strings.Builder
	emitted := false
	msgID := randomOCID()

	for {
		type readResult struct {
			msgType string
			data    json.RawMessage
			err     error
		}
		ch := make(chan readResult, 1)
		go func() {
			var rawMsg json.RawMessage
			err := conn.ReadJSON(&rawMsg)
			if err != nil {
				ch <- readResult{"", nil, err}
				return
			}
			var msg struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(rawMsg, &msg); err != nil {
				ch <- readResult{"", nil, err}
				return
			}
			ch <- readResult{msg.Type, rawMsg, nil}
		}()

		select {
		case <-ctx.Done():
			_ = conn.Close()
			return events, usage, ctx.Err()
		case r := <-ch:
			if r.err != nil {
				log.Printf("[DSH CLIENT] WebSocket read error: %v", r.err)
				return events, usage, fmt.Errorf("ws read: %w", r.err)
			}

			switch r.msgType {
			case "item":
				var item remoteMuxItemMessage
				if err := json.Unmarshal(r.data, &item); err != nil {
					log.Printf("[DSH CLIENT] Failed to unmarshal item: %v", err)
					continue
				}
				if item.StreamID != streamID {
					continue
				}
				// Item value contains the session event
				log.Printf("[DSH CLIENT] Received item: %s", string(item.Value))
				ev, parseErr := parseSessionEventFromRemote(item.Value)
				if parseErr != nil {
					log.Printf("[DSH CLIENT] Parse error: %v", parseErr)
					continue
				}
				log.Printf("[DSH CLIENT] Parsed event type: %s", ev.Type)
				mapped, mapUsage, done := c.processSessionEvent(ev, sessionID, &accumulated, &emitted, msgID)
				if mapUsage != nil {
					usage = mapUsage
				}
				if done {
					log.Printf("[DSH CLIENT] Turn completed (done=true), closing WebSocket")
					// Close the WebSocket connection to signal turn end
					conn.Close()
					// Small delay to ensure close is processed
					time.Sleep(100 * time.Millisecond)
					return events, usage, nil
				}
				if mapped != nil {
					events = append(events, mapped...)
				}

			case "end":
				var end remoteMuxEndMessage
				if err := json.Unmarshal(r.data, &end); err != nil {
					continue
				}
				if end.StreamID != streamID {
					continue
				}
				// Stream ended - emit final accumulated text if any
				if !emitted && accumulated.Len() > 0 {
					mapped, mapErr := agentMessageEvent(randomOCID(), msgID, accumulated.String())
					if mapErr == nil {
						events = append(events, mapped)
					}
				}
				return events, usage, nil

			case "error":
				var errMsg remoteMuxErrorMessage
				if err := json.Unmarshal(r.data, &errMsg); err != nil {
					continue
				}
				if errMsg.StreamID != streamID {
					continue
				}
				return events, usage, fmt.Errorf("remote.mux error: %s: %s",
					errMsg.Error.Code, errMsg.Error.Message)
			}
		}
	}
}

// parseSessionEventFromRemote extracts a session event from the remote.mux item value.
// session/follow returns frames with structure: { type: "event", event: { type, seq, data } }
func parseSessionEventFromRemote(value json.RawMessage) (*dshSessionEvent, error) {
	// First, try to parse as session/follow frame
	var frame sessionFollowFrame
	if err := json.Unmarshal(value, &frame); err == nil && frame.Type == "event" {
		// Extract the inner session event
		var ev dshSessionEvent
		if err := json.Unmarshal(frame.Event, &ev); err != nil {
			return nil, fmt.Errorf("parse inner event: %w", err)
		}
		return &ev, nil
	}

	// Fallback: try to parse as direct session event (for backward compatibility)
	var ev dshSessionEvent
	if err := json.Unmarshal(value, &ev); err != nil {
		return nil, fmt.Errorf("parse session event: %w", err)
	}
	return &ev, nil
}

// processSessionEvent maps a dsh session event to OMA events.
func (c *DeepSeekClient) processSessionEvent(
	ev *dshSessionEvent,
	sessionID string,
	accumulated *strings.Builder,
	emitted *bool,
	msgID string,
) (events []json.RawMessage, usage *TurnUsage, done bool) {
	switch ev.Type {
	case "assistant/chunk":
		var d struct {
			Chunk struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"chunk"`
		}
		if json.Unmarshal(ev.Data, &d) != nil ||
			d.Chunk.Type != "text-delta" || d.Chunk.Text == "" {
			return nil, nil, false
		}
		accumulated.WriteString(d.Chunk.Text)
		mapped, mapErr := agentMessageEvent(randomOCID(), msgID, accumulated.String())
		if mapErr != nil {
			return nil, nil, false
		}
		*emitted = true
		return []json.RawMessage{mapped}, nil, false

	case "tool/call":
		var d struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(ev.Data, &d) != nil {
			return nil, nil, false
		}
		mapped, mapErr := agentToolUseEvent(d.Name, "")
		if mapErr != nil {
			return nil, nil, false
		}
		return []json.RawMessage{mapped}, nil, false

	case "tool/result":
		var d struct {
			Error *struct {
				Name string `json:"name"`
			} `json:"error"`
		}
		if json.Unmarshal(ev.Data, &d) != nil {
			return nil, nil, false
		}
		content := "(completed)"
		if d.Error != nil {
			content = "(failed: " + d.Error.Name + ")"
		}
		mapped, mapErr := agentToolResultEvent("", content)
		if mapErr != nil {
			return nil, nil, false
		}
		return []json.RawMessage{mapped}, nil, false

	case "assistant/message":
		log.Printf("[DSH CLIENT] Raw assistant/message data: %s", string(ev.Data))
		var d struct {
			Message struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
			Usage *struct {
				InputTokens  int `json:"inputTokens"`
				OutputTokens int `json:"outputTokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(ev.Data, &d); err != nil {
			log.Printf("[DSH CLIENT] assistant/message parse error: %v", err)
			log.Printf("[DSH CLIENT] raw data: %s", string(ev.Data))
			return nil, nil, false
		}
		var sb strings.Builder
		for _, part := range d.Message.Content {
			if part.Type == "text" {
				sb.WriteString(part.Text)
			}
		}
		var result []json.RawMessage
		if sb.Len() > 0 {
			mapped, mapErr := agentMessageEvent(randomOCID(), msgID, sb.String())
			if mapErr != nil {
				return nil, nil, false
			}
			result = append(result, mapped)
			*emitted = true
		}
		var mapUsage *TurnUsage
		if d.Usage != nil {
			mapUsage = &TurnUsage{
				InputTokens:  d.Usage.InputTokens,
				OutputTokens: d.Usage.OutputTokens,
			}
		}
		// session/follow is a continuous observation stream that doesn't send turn/end.
		// When we receive assistant/message (the final response), the turn is complete.
		// deepseek-harness sends assistant/message with usage data when available,
		// but some responses may not include usage - still consider the turn done.
		log.Printf("[DSH CLIENT] assistant/message: text=%d chars, hasUsage=%v (input=%d, output=%d)",
			sb.Len(), d.Usage != nil,
			func() int { if d.Usage != nil { return d.Usage.InputTokens }; return 0 }(),
			func() int { if d.Usage != nil { return d.Usage.OutputTokens }; return 0 }())
		done := true // Always consider turn done after assistant/message
		return result, mapUsage, done

	case "turn/end":
		var d struct {
			Reason struct {
				Kind string `json:"kind"`
			} `json:"reason"`
		}
		_ = json.Unmarshal(ev.Data, &d)
		if !*emitted && accumulated.Len() > 0 {
			mapped, mapErr := agentMessageEvent(randomOCID(), msgID, accumulated.String())
			if mapErr == nil {
				events = append(events, mapped)
			}
		}
		if d.Reason.Kind == "error" {
			// Return with error indication
			return events, nil, true
		}
		return events, nil, true
	}

	return nil, nil, false
}

// RunTurnStream implements StreamingClient. It prompts the dsh session
// over RPC, then consumes /api/remote.mux until turn/end, mapping each
// SessionEvent onto the oma vocabulary as it arrives.
func (c *DeepSeekClient) RunTurnStream(
	ctx context.Context,
	req TurnRequest,
	onEvent EventHandler,
) error {
	start := time.Now()
	userText := extractLastUserMessage(req.Events)
	if userText == "" {
		userText = "(continue)"
	}

	if err := c.ensureSession(ctx, req.SessionID); err != nil {
		return err
	}

	if err := c.rpc(ctx, "session.prompt", map[string]any{
		"request": map[string]any{
			"requestId": randomOCID(),
			"sessionId": req.SessionID,
			"mode":      "queue",
			"content":   []map[string]any{{"type": "text", "text": userText}},
		},
	}, nil); err != nil {
		return err
	}

	// Use the correct Typert RPC streaming endpoint
	baseURL := strings.TrimRight(c.GatewayURL, "/")
	wsURL := strings.Replace(baseURL, "http", "ws", 1) + "/api/remote.mux"
	header := http.Header{}
	if c.Token != "" {
		header.Set("Authorization", "Bearer "+c.Token)
	}
	// Add dsh browser-auth cookie if available
	if cookie := c.getAuthCookie(); cookie != nil {
		header.Set("Cookie", cookie.Name+"="+cookie.Value)
	}
	dialer := websocket.DefaultDialer
	conn, _, err := dialer.DialContext(ctx, wsURL, header)
	if err != nil {
		return fmt.Errorf("deepseek remote.mux dial: %w", err)
	}
	defer conn.Close()

	streamID := randomOCID()

	// Open the session/follow stream
	// session/follow expects: Payload.args.request.address (SessionFollowRequest)
	// address must be { kind: "session", sessionId: "..." } format
	openMsg := remoteMuxOpenMessage{
		Type:     "open",
		StreamID: streamID,
		Endpoint: "session/follow",
		Payload: map[string]any{
			"args": map[string]any{
				"request": map[string]any{
					"address": map[string]any{
						"kind":      "session",
						"sessionId": req.SessionID,
					},
				},
			},
		},
	}
	if err := conn.WriteJSON(openMsg); err != nil {
		return fmt.Errorf("remote.mux open: %w", err)
	}

	var accumulated strings.Builder
	emitted := false
	msgID := randomOCID()
	turnDone := false
	var usage *TurnUsage

	for {
		// If turn is done, emit span.model_request_end and close
		if turnDone {
			time.Sleep(200 * time.Millisecond)
			log.Printf("[DSH CLIENT] Turn done, emitting span.model_request_end and closing WebSocket")

			// Emit span.model_request_end for usage tracking
			duration := time.Since(start)
			if usageEv, err := usageEvent("deepseek-chat", "deepseek", duration, usage); err == nil {
				if err := onEvent(usageEv); err != nil {
					return err
				}
			}

			_ = conn.Close()
			logTurn("backend", "deepseek", "session", req.SessionID,
				"stream", true,
				"duration_ms", duration.Milliseconds(),
				"chars", accumulated.Len(),
				"input_tokens", valueOrZero(usage, func(u *TurnUsage) int { return u.InputTokens }),
				"output_tokens", valueOrZero(usage, func(u *TurnUsage) int { return u.OutputTokens }))
			return nil
		}

		type readResult struct {
			msgType string
			data    json.RawMessage
			err     error
		}
		ch := make(chan readResult, 1)
		go func() {
			var rawMsg json.RawMessage
			err := conn.ReadJSON(&rawMsg)
			if err != nil {
				ch <- readResult{"", nil, err}
				return
			}
			var msg struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(rawMsg, &msg); err != nil {
				ch <- readResult{"", nil, err}
				return
			}
			ch <- readResult{msg.Type, rawMsg, nil}
		}()

		select {
		case <-ctx.Done():
			_ = conn.Close()
			return ctx.Err()
		case r := <-ch:
			if r.err != nil {
				return fmt.Errorf("ws read: %w", r.err)
			}

			switch r.msgType {
			case "item":
				var item remoteMuxItemMessage
				if err := json.Unmarshal(r.data, &item); err != nil {
					continue
				}
				if item.StreamID != streamID {
					continue
				}
				ev, parseErr := parseSessionEventFromRemote(item.Value)
				if parseErr != nil {
					continue
				}
				done, emitErr := c.emitSessionEvent(ev, &accumulated, &emitted, msgID, onEvent, &usage)
				if emitErr != nil {
					return emitErr
				}
				if done {
					turnDone = true
				}

			case "end":
				var end remoteMuxEndMessage
				if err := json.Unmarshal(r.data, &end); err != nil {
					continue
				}
				if end.StreamID != streamID {
					continue
				}
				if !emitted && accumulated.Len() > 0 {
					mapped, mapErr := agentMessageEvent(randomOCID(), msgID, accumulated.String())
					if mapErr != nil {
						return mapErr
					}
					if err := onEvent(mapped); err != nil {
						return err
					}
				}
				logTurn("backend", "deepseek", "session", req.SessionID,
					"stream", true,
					"duration_ms", time.Since(start).Milliseconds(),
					"chars", accumulated.Len())
				return nil

			case "error":
				var errMsg remoteMuxErrorMessage
				if err := json.Unmarshal(r.data, &errMsg); err != nil {
					continue
				}
				if errMsg.StreamID != streamID {
					continue
				}
				return fmt.Errorf("remote.mux error: %s: %s",
					errMsg.Error.Code, errMsg.Error.Message)
			}
		}
	}
}

// emitSessionEvent processes and emits a single session event to the handler.
// It returns (done, err) where done=true indicates the turn is complete.
// It also updates the usage pointer if token usage is received.
func (c *DeepSeekClient) emitSessionEvent(
	ev *dshSessionEvent,
	accumulated *strings.Builder,
	emitted *bool,
	msgID string,
	onEvent EventHandler,
	// usage is updated if the assistant/message includes usage data
	usage **TurnUsage,
) (done bool, err error) {
	switch ev.Type {
	case "assistant/chunk":
		var d struct {
			Chunk struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"chunk"`
		}
		if json.Unmarshal(ev.Data, &d) != nil ||
			d.Chunk.Type != "text-delta" || d.Chunk.Text == "" {
			return false, nil
		}
		accumulated.WriteString(d.Chunk.Text)
		mapped, mapErr := agentMessageEvent(randomOCID(), msgID, accumulated.String())
		if mapErr != nil {
			return false, mapErr
		}
		if err := onEvent(mapped); err != nil {
			return false, err
		}
		*emitted = true
		return false, nil

	case "tool/call":
		var d struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(ev.Data, &d) != nil {
			return false, nil
		}
		mapped, mapErr := agentToolUseEvent(d.Name, "")
		if mapErr != nil {
			return false, mapErr
		}
		if err := onEvent(mapped); err != nil {
			return false, err
		}
		return false, nil

	case "tool/result":
		var d struct {
			Error *struct {
				Name string `json:"name"`
			} `json:"error"`
		}
		if json.Unmarshal(ev.Data, &d) != nil {
			return false, nil
		}
		content := "(completed)"
		if d.Error != nil {
			content = "(failed: " + d.Error.Name + ")"
		}
		mapped, mapErr := agentToolResultEvent("", content)
		if mapErr != nil {
			return false, mapErr
		}
		if err := onEvent(mapped); err != nil {
			return false, err
		}
		return false, nil

	case "assistant/message":
		var d struct {
			Message struct {
				Content []struct {
					Type      string          `json:"type"`
					Text      string          `json:"text"`
					ID        string          `json:"id"`
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				} `json:"content"`
			} `json:"message"`
			Usage *struct {
				InputTokens  int `json:"inputTokens"`
				OutputTokens int `json:"outputTokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(ev.Data, &d); err != nil {
			return false, nil
		}

		// Process each content item - emit tool calls and accumulate text
		var textContent strings.Builder
		hasToolCall := false
		for _, part := range d.Message.Content {
			switch part.Type {
			case "text":
				textContent.WriteString(part.Text)
			case "tool-call":
				// Emit agent.tool_use event for tool calls
				hasToolCall = true
				mapped, mapErr := agentToolUseEvent(part.Name, "")
				if mapErr != nil {
					return false, mapErr
				}
				if err := onEvent(mapped); err != nil {
					return false, err
				}
				*emitted = true
			case "reasoning":
				// Skip reasoning content - it's internal thinking
			}
		}

		// Emit accumulated text as agent.message (final response after tool execution)
		if textContent.Len() > 0 {
			mapped, mapErr := agentMessageEvent(randomOCID(), msgID, textContent.String())
			if mapErr != nil {
				return false, mapErr
			}
			if err := onEvent(mapped); err != nil {
				return false, err
			}
			*emitted = true
		}

		// Capture usage data if present
		if d.Usage != nil && *usage == nil {
			*usage = &TurnUsage{
				InputTokens:  d.Usage.InputTokens,
				OutputTokens: d.Usage.OutputTokens,
			}
		}

		// Only mark turn done if this is a final text response (no tool calls)
		// Tool calls mean the model is requesting tool execution, not completing
		if hasToolCall {
			return false, nil
		}
		return true, nil

	case "turn/end":
		// Turn end is handled by the stream end message
		return false, nil
	}

	return false, nil
}
