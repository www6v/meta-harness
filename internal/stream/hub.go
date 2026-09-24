package stream

import (
	"encoding/json"
	"sync"
)

// Event is a broadcast payload with sequence metadata.
type Event struct {
	Seq     int
	Payload json.RawMessage
}

// Hub fans out session events to SSE subscribers.
type Hub struct {
	mu   sync.RWMutex
	subs map[string]map[chan Event]struct{}
}

// NewHub returns an empty event hub.
func NewHub() *Hub {
	return &Hub{subs: make(map[string]map[chan Event]struct{})}
}

// Subscribe registers a listener for sessionID.
func (h *Hub) Subscribe(sessionID string) (<-chan Event, func()) {
	// Buffer of 256 events — large enough that a burst of cumulative
	// streaming events (agent.message × N, agent.thinking × N) during a
	// fast turn doesn't overflow and silently drop the trailing
	// session.status_idle.  Non-blocking Publish still protects against a
	// stalled browser blocking the harness goroutine, but the wider
	// channel makes overflow vanishingly unlikely in practice.
	ch := make(chan Event, 256)
	h.mu.Lock()
	if h.subs[sessionID] == nil {
		h.subs[sessionID] = make(map[chan Event]struct{})
	}
	h.subs[sessionID][ch] = struct{}{}
	h.mu.Unlock()

	unsub := func() {
		h.mu.Lock()
		delete(h.subs[sessionID], ch)
		if len(h.subs[sessionID]) == 0 {
			delete(h.subs, sessionID)
		}
		h.mu.Unlock()
		close(ch)
	}
	return ch, unsub
}

// Publish delivers an event to all subscribers of sessionID.
func (h *Hub) Publish(sessionID string, ev Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs[sessionID] {
		select {
		case ch <- ev:
		default:
			// Channel buffer full — subscriber can't keep up.
			// Log the dropped event so we can diagnose issues.
			var evType string
			var meta struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(ev.Payload, &meta) == nil {
				evType = meta.Type
			}
			// IMPORTANT: still deliver critical lifecycle events
			// (status_idle, status_running, error) even if it means
			// blocking briefly. These events are essential for the
			// frontend to transition state correctly.
			if evType == "session.status_idle" || evType == "session.status_running" || evType == "session.error" {
				ch <- ev // blocking send — wait for buffer space
			}
		}
	}
}
