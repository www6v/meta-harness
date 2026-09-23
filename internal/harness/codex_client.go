package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// CodexClient is a harness.Client that runs turns against a remote
// codex app-server via the Python bridge
// (meta-harness-ext/ssh/codex_bridge.py). The bridge speaks JSON-RPC
// 2.0 over WebSocket and (optionally) SSH-tunnels to the codex
// container.
//
// Call graph:
//
//	oma-server  --HTTP-->  bridge :8092/codex/turn  --WS/SSH-->  codex :5432
//
// The bridge returns OMA-shaped events (agent.message, agent.thinking,
// agent.tool_use, agent.tool_result, session.error) which we forward
// verbatim into the session event log.
type CodexClient struct {
	// BridgeURL is the HTTP base of the Python bridge, e.g.
	// "http://127.0.0.1:8092". No trailing slash.
	BridgeURL string
	// HTTP is the optional *http.Client override. When nil a default
	// client with a 10-minute timeout is used — codex turns can take
	// a while when the agent does multi-step tool calls.
	HTTP *http.Client
}

// codexBridgeTurnRequest is the JSON body POSTed to the bridge.
// We only forward the fields the bridge actually reads
// (session_id, agent.system_prompt / system, events) — everything
// else is dropped. The bridge pulls the latest user.message text
// from events and feeds it to codex.
type codexBridgeTurnRequest struct {
	SessionID string          `json:"session_id"`
	Agent     json.RawMessage `json:"agent,omitempty"`
	Events    json.RawMessage `json:"events,omitempty"`
}

// codexBridgeTurnResponse is the JSON body returned by the bridge.
type codexBridgeTurnResponse struct {
	Events []json.RawMessage `json:"events"`
	Usage  *TurnUsage        `json:"usage,omitempty"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// RunTurn implements Client. It POSTs to the bridge, decodes the
// response, and returns the events verbatim.
func (c *CodexClient) RunTurn(
	ctx context.Context,
	req TurnRequest,
) (TurnResponse, error) {
	start := time.Now()

	// Marshal the bits the bridge cares about.
	agentRaw, err := json.Marshal(req.Agent)
	if err != nil {
		return TurnResponse{}, fmt.Errorf("codex marshal agent: %w", err)
	}
	eventsRaw, err := json.Marshal(req.Events)
	if err != nil {
		return TurnResponse{}, fmt.Errorf("codex marshal events: %w", err)
	}
	body, err := json.Marshal(codexBridgeTurnRequest{
		SessionID: req.SessionID,
		Agent:     agentRaw,
		Events:    eventsRaw,
	})
	if err != nil {
		return TurnResponse{}, fmt.Errorf("codex marshal body: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost,
		c.BridgeURL+"/codex/turn",
		bytes.NewReader(body),
	)
	if err != nil {
		return TurnResponse{}, fmt.Errorf("codex build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := c.httpClient()
	resp, err := client.Do(httpReq)
	if err != nil {
		logTurn("backend", "codex", "session", req.SessionID,
			"duration_ms", time.Since(start).Milliseconds(),
			"error", err)
		return TurnResponse{}, fmt.Errorf("codex bridge: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024))
	if err != nil {
		return TurnResponse{}, fmt.Errorf("codex read body: %w", err)
	}

	if resp.StatusCode >= 300 {
		logTurn("backend", "codex", "session", req.SessionID,
			"duration_ms", time.Since(start).Milliseconds(),
			"status", resp.StatusCode,
			"error", strings.TrimSpace(string(raw)))
		return TurnResponse{}, fmt.Errorf(
			"codex bridge status=%d: %s",
			resp.StatusCode,
			strings.TrimSpace(string(raw)),
		)
	}

	var out codexBridgeTurnResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return TurnResponse{}, fmt.Errorf("codex decode response: %w", err)
	}
	if out.Error != nil && out.Error.Message != "" {
		return TurnResponse{}, fmt.Errorf("codex bridge: %s", out.Error.Message)
	}

	duration := time.Since(start)
	logTurn("backend", "codex", "session", req.SessionID,
		"duration_ms", duration.Milliseconds(),
		"events", len(out.Events),
		"input_tokens", valueOrZero(out.Usage, func(u *TurnUsage) int { return u.InputTokens }),
		"output_tokens", valueOrZero(out.Usage, func(u *TurnUsage) int { return u.OutputTokens }),
	)
	return TurnResponse{Events: out.Events, Usage: out.Usage}, nil
}

func (c *CodexClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 10 * time.Minute}
}
