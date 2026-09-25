package harness

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
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
// (session_id, agent.system_prompt / system, events, skills, sub_agents) —
// everything else is dropped. The bridge pulls the latest user.message
// text from events and feeds it to codex. Skills are resolved by the
// ResourceResolver into AMA-shaped payloads (with system_prompt_addition
// + files) — the bridge writes them into the codex workspace and
// injects their prompt additions into the turn instructions. Sub-agents
// are the resolved callable agent snapshots; the bridge describes them
// to the codex model so it can decide when to spawn them.
type codexBridgeTurnRequest struct {
	SessionID string          `json:"session_id"`
	Agent     json.RawMessage `json:"agent,omitempty"`
	Events    json.RawMessage `json:"events,omitempty"`
	Skills    json.RawMessage `json:"skills,omitempty"`
	SubAgents json.RawMessage `json:"sub_agents,omitempty"`
}

// codexBridgeTurnResponse is the JSON body returned by the bridge.
type codexBridgeTurnResponse struct {
	Events []json.RawMessage `json:"events"`
	Usage  *TurnUsage        `json:"usage,omitempty"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
	// Files extracted from codex during the turn (fileChange items).
	Files []codexFileOutput `json:"files,omitempty"`
}

// codexFileOutput is a file extracted from a codex turn by the Python bridge.
type codexFileOutput struct {
	Filename      string `json:"filename"`
	Path          string `json:"path"`
	ContentBase64 string `json:"content_base64"`
	MediaType     string `json:"media_type"`
	SizeBytes     int    `json:"size_bytes"`
}

// codexUploadedFile is the file metadata after uploading to the OMA Files API.
type codexUploadedFile struct {
	FileID      string `json:"id"`
	Filename    string `json:"filename"`
	MediaType   string `json:"media_type"`
	SizeBytes   int    `json:"size_bytes"`
	DownloadURL string `json:"download_url"`
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
	var skillsRaw json.RawMessage
	if len(req.Skills) > 0 {
		skillsRaw, err = json.Marshal(req.Skills)
		if err != nil {
			return TurnResponse{}, fmt.Errorf("codex marshal skills: %w", err)
		}
	}
	var subAgentsRaw json.RawMessage
	if len(req.SubAgents) > 0 {
		subAgentsRaw, err = json.Marshal(req.SubAgents)
		if err != nil {
			return TurnResponse{}, fmt.Errorf("codex marshal sub_agents: %w", err)
		}
	}
	body, err := json.Marshal(codexBridgeTurnRequest{
		SessionID: req.SessionID,
		Agent:     agentRaw,
		Events:    eventsRaw,
		Skills:    skillsRaw,
		SubAgents: subAgentsRaw,
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
		"files", len(out.Files),
		"input_tokens", valueOrZero(out.Usage, func(u *TurnUsage) int { return u.InputTokens }),
		"output_tokens", valueOrZero(out.Usage, func(u *TurnUsage) int { return u.OutputTokens }),
	)

	// Upload files to the OMA Files API and enrich events with file metadata.
	var uploaded []codexUploadedFile
	if len(out.Files) > 0 && req.PlatformBase != "" {
		uploaded = c.uploadFiles(ctx, req, out.Files)
		if len(uploaded) > 0 {
			out.Events = c.attachFilesToEvents(out.Events, uploaded)
		}
	} else if len(out.Files) > 0 && req.PlatformBase == "" {
		log.Printf("codex: skipping file upload: PlatformBase not configured")
	}

	return TurnResponse{Events: out.Events, Usage: out.Usage}, nil
}

func (c *CodexClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 10 * time.Minute}
}

// streamingHTTPClient returns an HTTP client with no overall timeout,
// suitable for SSE streaming where the response body is read incrementally
// and the connection may stay open for the full duration of a codex turn
// (which can take 10+ minutes for complex multi-step tool-use turns).
// The per-request context from the caller still controls cancellation.
func (c *CodexClient) streamingHTTPClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 0} // no timeout; context controls cancellation
}

// RunTurnStream implements StreamingClient. It calls the bridge's SSE
// streaming endpoint (/codex/turn/sse) so events are emitted via onEvent
// as the codex model produces them — giving the frontend a live streaming
// effect instead of a single batch at turn-end.
func (c *CodexClient) RunTurnStream(
	ctx context.Context,
	req TurnRequest,
	onEvent EventHandler,
) error {
	body, err := json.Marshal(c.buildBridgeRequest(req))
	if err != nil {
		return fmt.Errorf("codex marshal body: %w", err)
	}
	return c.streamTurnHTTP(ctx, body, onEvent)
}

// streamTurnHTTP POSTs to the bridge's /codex/turn/sse endpoint and reads
// SSE frames, decoding each `data:` line as a JSON event and invoking
// onEvent. Falls back to batch RunTurn if the bridge is too old to serve
// the streaming endpoint.
func (c *CodexClient) streamTurnHTTP(
	ctx context.Context,
	body []byte,
	onEvent EventHandler,
) error {
	client := c.streamingHTTPClient()
	url := c.BridgeURL + "/codex/turn/sse"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("codex build sse request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(httpReq)
	if err != nil {
		// Streaming endpoint not available (old bridge) — fall back to batch.
		log.Printf("codex: SSE endpoint unavailable (%v), falling back to batch", err)
		return c.fallbackBatchStream(ctx, body, onEvent)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		// If the bridge doesn't support streaming (404), fall back to batch.
		if resp.StatusCode == http.StatusNotFound {
			log.Printf("codex: SSE endpoint not found, falling back to batch")
			return c.fallbackBatchStream(ctx, body, onEvent)
		}
		return fmt.Errorf("codex bridge sse status=%d: %s", resp.StatusCode, string(respBody))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	var eventBuf strings.Builder
	lastEventAt := time.Now()
	// Inactivity timeout: codex turns can have long pauses between events
	// (e.g. during tool execution, complex reasoning, or file I/O).  10s
	// was too aggressive — a pause longer than 10s caused the Go reader
	// to close the stream prematurely, discarding all subsequent
	// cumulative events.  The Python bridge sends periodic keepalive
	// comments (every 5s) to prevent proxy timeouts, so a 60s inactivity
	// window is a safe upper bound for "the bridge is truly stuck".
	noProgressTimeout := 60 * time.Second
	// Overall stream timeout: if the stream doesn't close within 10 minutes,
	// the bridge is likely stuck. This is a safety net for cases where the
	// codex turn completes but the bridge never signals "done".
	streamDeadline := time.Now().Add(10 * time.Minute)
	eventCount := 0
	log.Printf("codex: SSE stream started")

	// Use a channel to read lines with a timeout, so we can detect when
	// the bridge stops sending data (e.g., after the poison pill).
	type scanResult struct {
		line string
		ok   bool
		err  error
	}
	lineCh := make(chan scanResult, 1)
	go func() {
		for scanner.Scan() {
			lineCh <- scanResult{line: scanner.Text(), ok: true}
		}
		lineCh <- scanResult{ok: false, err: scanner.Err()}
	}()

	for {
		select {
		case result := <-lineCh:
			if !result.ok {
				if result.err != nil {
					log.Printf("codex: SSE scanner error: %v", result.err)
					return fmt.Errorf("codex sse read: %w", result.err)
				}
				log.Printf("codex: SSE stream ended normally, received %d events", eventCount)
				// Handle last frame if no trailing blank line.
				if eventBuf.Len() > 0 {
					var ev json.RawMessage
					if err := json.Unmarshal([]byte(eventBuf.String()), &ev); err == nil {
						_ = onEvent(ev)
					}
				}
				return nil
			}
			line := result.line
			if time.Now().After(streamDeadline) {
				log.Printf("codex: SSE stream exceeded 10min deadline, aborting")
				return fmt.Errorf("codex sse: stream exceeded 10min deadline")
			}
			if line == "" {
				// Blank line = end of SSE frame.  Reset the inactivity
				// timer for ALL frames — including keepalive comments
				// (": keepalive\n\n") from the Python bridge — so that
				// legitimate pauses in codex event production (e.g.
				// during tool execution) don't prematurely close the
				// stream.  Previously this was inside the
				// eventBuf.Len() > 0 block, which meant keepalives
				// didn't reset the timer.
				lastEventAt = time.Now()
				if eventBuf.Len() > 0 {
					var ev json.RawMessage
					if err := json.Unmarshal([]byte(eventBuf.String()), &ev); err == nil {
						eventCount++
						log.Printf("codex: SSE event #%d received: %s", eventCount, string(ev)[:min(100, len(ev))])
						if err := onEvent(ev); err != nil {
							log.Printf("codex: SSE onEvent error: %v", err)
							return err
						}
					} else {
						log.Printf("codex: SSE unmarshal error: %v, data: %s", err, eventBuf.String())
					}
					eventBuf.Reset()
				}
				continue
			}
			if strings.HasPrefix(line, "data: ") {
				eventBuf.WriteString(strings.TrimPrefix(line, "data: "))
			}
			// Skip "event:", "id:", and ":" (keepalive) lines.

		case <-time.After(noProgressTimeout):
			if eventCount > 0 {
				log.Printf("codex: SSE no progress for %v (last event %v ago), closing stream with %d events", noProgressTimeout, time.Since(lastEventAt), eventCount)
				return nil // treat as successful completion
			}
			log.Printf("codex: SSE timeout waiting for first event after %v", noProgressTimeout)
			return fmt.Errorf("codex sse: no events received within %v", noProgressTimeout)
		}
	}
}

// buildBridgeRequest marshals the TurnRequest into the JSON shape the
// Python bridge expects. Shared between RunTurn (batch) and RunTurnStream
// (SSE) so the request payload is identical regardless of streaming mode.
func (c *CodexClient) buildBridgeRequest(req TurnRequest) codexBridgeTurnRequest {
	agentRaw, err := json.Marshal(req.Agent)
	if err != nil {
		log.Printf("codex marshal agent: %v", err)
		agentRaw = json.RawMessage("{}")
	}
	eventsRaw, err := json.Marshal(req.Events)
	if err != nil {
		log.Printf("codex marshal events: %v", err)
		eventsRaw = json.RawMessage("[]")
	}
	var skillsRaw json.RawMessage
	if len(req.Skills) > 0 {
		skillsRaw, _ = json.Marshal(req.Skills)
	}
	var subAgentsRaw json.RawMessage
	if len(req.SubAgents) > 0 {
		subAgentsRaw, _ = json.Marshal(req.SubAgents)
	}
	return codexBridgeTurnRequest{
		SessionID: req.SessionID,
		Agent:     agentRaw,
		Events:    eventsRaw,
		Skills:    skillsRaw,
		SubAgents: subAgentsRaw,
	}
}

// fallbackBatchStream calls the regular /codex/turn endpoint and iterates
// events after the turn completes. Used when the bridge is too old to
// support /codex/turn/sse.
func (c *CodexClient) fallbackBatchStream(
	ctx context.Context,
	body []byte,
	onEvent EventHandler,
) error {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BridgeURL+"/codex/turn", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("codex build fallback request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient().Do(httpReq)
	if err != nil {
		return fmt.Errorf("codex bridge fallback: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("codex read fallback body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("codex bridge fallback status=%d: %s", resp.StatusCode, string(respBody))
	}
	var out codexBridgeTurnResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return fmt.Errorf("codex decode fallback: %w", err)
	}
	for _, ev := range out.Events {
		if err := onEvent(ev); err != nil {
			return err
		}
	}
	return nil
}

// uploadFiles uploads each file to the OMA Files API (POST /v1/files).
// Returns the list of successfully uploaded files with their IDs and URLs.
func (c *CodexClient) uploadFiles(
	ctx context.Context,
	req TurnRequest,
	files []codexFileOutput,
) []codexUploadedFile {
	var uploaded []codexUploadedFile
	for _, f := range files {
		if f.ContentBase64 == "" || f.Filename == "" {
			continue
		}
		// Build the JSON upload body.
		body, err := json.Marshal(map[string]any{
			"filename":     f.Filename,
			"content":      f.ContentBase64,
			"encoding":     "base64",
			"media_type":   f.MediaType,
			"scope_id":     req.SessionID,
			"downloadable": true,
		})
		if err != nil {
			log.Printf("codex file upload marshal: %v", err)
			continue
		}
		url := strings.TrimRight(req.PlatformBase, "/") + "/v1/files"
		httpReq, err := http.NewRequestWithContext(
			ctx, http.MethodPost, url, bytes.NewReader(body),
		)
		if err != nil {
			log.Printf("codex file upload request: %v", err)
			continue
		}
		httpReq.Header.Set("Content-Type", "application/json")
		if req.TenantID != "" {
			httpReq.Header.Set("x-active-tenant", req.TenantID)
		}
		if req.InternalSecret != "" {
			httpReq.Header.Set("X-API-Key", req.InternalSecret)
		}
		client := c.httpClient()
		resp, err := client.Do(httpReq)
		if err != nil {
			log.Printf("codex file upload do: %v", err)
			continue
		}
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			log.Printf(
				"codex file upload status=%d: %s",
				resp.StatusCode,
				string(respBody),
			)
			continue
		}
		var record struct {
			ID        string `json:"id"`
			Filename  string `json:"filename"`
			MediaType string `json:"media_type"`
			SizeBytes int    `json:"size_bytes"`
		}
		if err := json.Unmarshal(respBody, &record); err != nil {
			log.Printf("codex file upload decode: %v", err)
			continue
		}
		uploaded = append(uploaded, codexUploadedFile{
			FileID:      record.ID,
			Filename:    record.Filename,
			MediaType:   record.MediaType,
			SizeBytes:   record.SizeBytes,
			DownloadURL: fmt.Sprintf("/v1/files/%s/content", record.ID),
		})
	}
	if len(uploaded) > 0 {
		log.Printf(
			"codex uploaded %d/%d files session=%s",
			len(uploaded),
			len(files),
			req.SessionID,
		)
	}
	return uploaded
}

// attachFilesToEvents enriches the last agent.tool_result event with a
// "files" field containing the uploaded file metadata. It looks for the
// last tool_result with name="edit" first (for fileChange items), then
// falls back to the last tool_result with name="bash" (for bash-created
// files), and finally falls back to the very last tool_result.
func (c *CodexClient) attachFilesToEvents(
	events []json.RawMessage,
	files []codexUploadedFile,
) []json.RawMessage {
	if len(files) == 0 {
		return events
	}
	// Build the files array for the event.
	fileInfos := make([]map[string]any, 0, len(files))
	for _, f := range files {
		fileInfos = append(fileInfos, map[string]any{
			"filename":     f.Filename,
			"file_id":      f.FileID,
			"media_type":   f.MediaType,
			"size_bytes":   f.SizeBytes,
			"download_url": f.DownloadURL,
		})
	}
	// First pass: look for agent.tool_result with name="edit" (fileChange items).
	for i := len(events) - 1; i >= 0; i-- {
		var ev map[string]any
		if err := json.Unmarshal(events[i], &ev); err != nil {
			continue
		}
		if ev["type"] == "agent.tool_result" && ev["name"] == "edit" {
			ev["files"] = fileInfos
			enriched, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			events[i] = enriched
			return events
		}
	}
	// Second pass: look for agent.tool_result with name="bash" (bash-created files).
	for i := len(events) - 1; i >= 0; i-- {
		var ev map[string]any
		if err := json.Unmarshal(events[i], &ev); err != nil {
			continue
		}
		if ev["type"] == "agent.tool_result" && ev["name"] == "bash" {
			ev["files"] = fileInfos
			enriched, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			events[i] = enriched
			return events
		}
	}
	// Third pass: attach to the very last agent.tool_result.
	for i := len(events) - 1; i >= 0; i-- {
		var ev map[string]any
		if err := json.Unmarshal(events[i], &ev); err != nil {
			continue
		}
		if ev["type"] == "agent.tool_result" {
			ev["files"] = fileInfos
			enriched, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			events[i] = enriched
			return events
		}
	}
	return events
}

// codexWorkdirOutputs returns the outputs directory path within a workdir.
func codexWorkdirOutputs(workdir string) string {
	return filepath.Join(workdir, "outputs")
}
