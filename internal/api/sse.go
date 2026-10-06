// Package api exposes the HTTP surface: setup, auth, dashboard, uploads, media,
// playlists, schedules, stream control, logs and settings (PRD §13).
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/wisnu/ylivemasadi/internal/logging"
	"github.com/wisnu/ylivemasadi/internal/streaming"
)

// Event kinds delivered over SSE.
const (
	kindMetrics     = "metrics"
	kindStreamState = "stream_state"
	kindLog         = "log"
	kindMedia       = "media"
	kindSchedule    = "schedule"
	kindAlert       = "alert"
)

// envelope is the wire format for every SSE message.
type envelope struct {
	Kind    string `json:"kind"`
	Payload any    `json:"payload"`
}

// client is one connected browser.
type client struct {
	ch     chan []byte
	closed chan struct{}
}

// Hub fans server events out to connected browsers (FR-02).
type Hub struct {
	mu      sync.RWMutex
	clients map[*client]struct{}
	log     *logging.Logger
}

// NewHub creates an empty hub.
func NewHub(log *logging.Logger) *Hub {
	return &Hub{clients: map[*client]struct{}{}, log: log}
}

// PublishLog forwards a log entry to every client (FR-09).
func (h *Hub) PublishLog(entry logging.Entry) {
	h.send(kindLog, entry)
}

// PublishStreamState forwards session progress (FR-02, FR-08).
func (h *Hub) PublishStreamState(p streaming.Progress) {
	h.send(kindStreamState, p)
}

// PublishMetrics forwards a monitoring sample.
func (h *Hub) PublishMetrics(m any) { h.send(kindMetrics, m) }

// PublishMedia signals that a media file changed state.
func (h *Hub) PublishMedia(v any) { h.send(kindMedia, v) }

// PublishSchedule signals that a schedule changed.
func (h *Hub) PublishSchedule(v any) { h.send(kindSchedule, v) }

// PublishAlert pushes an operator-facing message.
func (h *Hub) PublishAlert(v any) { h.send(kindAlert, v) }

// send marshals once and delivers to every client without blocking: a slow or
// wedged browser must never stall the publisher.
func (h *Hub) send(kind string, payload any) {
	data, err := json.Marshal(envelope{Kind: kind, Payload: payload})
	if err != nil {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		select {
		case c.ch <- data:
		default:
			// The client is not keeping up; drop rather than block the server.
		}
	}
}

// Count reports the number of connected clients.
func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// Serve handles GET /api/events with an authenticated SSE connection.
//
// A periodic comment keeps intermediaries from closing an idle connection and
// lets the client notice a dead link promptly.
func (h *Hub) Serve(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	h.mu.Lock()
	c := &client{ch: make(chan []byte, 64), closed: make(chan struct{})}
	h.clients[c] = struct{}{}
	n := len(h.clients)
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		delete(h.clients, c)
		h.mu.Unlock()
		close(c.closed)
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Tell nginx-style proxies not to buffer the stream.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "event: ready\ndata: {\"clients\":%d}\n\n", n)
	flusher.Flush()

	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-c.closed:
			return
		case <-ticker.C:
			// A comment frame; browsers ignore it but it resets idle timers.
			if _, err := w.Write([]byte(": keep-alive\n\n")); err != nil {
				return
			}
			flusher.Flush()
		case data := <-c.ch:
			if _, err := w.Write([]byte("data: ")); err != nil {
				return
			}
			if _, err := w.Write(data); err != nil {
				return
			}
			if _, err := w.Write([]byte("\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// runPeriodic broadcasts a value on an interval until ctx ends.
func runPeriodic(ctx context.Context, interval time.Duration, fn func()) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fn()
		}
	}
}
