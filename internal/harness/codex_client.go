package harness

import (
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
// (session_id, agent.system_prompt / system, events, skills) —
// everything else is dropped. The bridge pulls the latest user.message
// text from events and feeds it to codex. Skills are resolved by the
// ResourceResolver into AMA-shaped payloads (with system_prompt_addition
// + files) — the bridge writes them into the codex workspace and
// injects their prompt additions into the turn instructions.
type codexBridgeTurnRequest struct {
	SessionID string          `json:"session_id"`
	Agent     json.RawMessage `json:"agent,omitempty"`
	Events    json.RawMessage `json:"events,omitempty"`
	Skills    json.RawMessage `json:"skills,omitempty"`
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
	body, err := json.Marshal(codexBridgeTurnRequest{
		SessionID: req.SessionID,
		Agent:     agentRaw,
		Events:    eventsRaw,
		Skills:    skillsRaw,
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

// RunTurnStream implements StreamingClient. It calls RunTurn (which handles
// file upload internally), then emits each event via onEvent.
func (c *CodexClient) RunTurnStream(
	ctx context.Context,
	req TurnRequest,
	onEvent EventHandler,
) error {
	resp, err := c.RunTurn(ctx, req)
	if err != nil {
		return err
	}
	for _, ev := range resp.Events {
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
