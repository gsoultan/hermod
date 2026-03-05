package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/user/hermod/internal/sse"
)

// handleSSEStream streams events from the data orchestration hub (GetDataHub).
// Registered at /streams/sse
// This endpoint is for SSESink clients.
func (s *Server) handleSSEStream(w http.ResponseWriter, r *http.Request) {
	s.serveSSE(w, r, sse.GetDataHub())
}

// handleInternalSSE streams events from the internal API hub (GetInternalHub).
// Registered at /api/notifications/sse
// This endpoint is for Hermod's own UI/management clients.
func (s *Server) handleInternalSSE(w http.ResponseWriter, r *http.Request) {
	s.serveSSE(w, r, sse.GetInternalHub())
}

func (s *Server) serveSSE(w http.ResponseWriter, r *http.Request, hub *sse.Hub) {
	stream := r.URL.Query().Get("stream")
	if stream == "" {
		stream = "default"
	}

	// Check stream configuration for security (only applies to DataHub streams usually,
	// but we check the hub passed in)
	if cfg, ok := hub.GetStreamConfig(stream); ok {
		// 1. Origin verification
		if len(cfg.AllowedOrigins) > 0 {
			origin := r.Header.Get("Origin")
			allowed := false
			for _, o := range cfg.AllowedOrigins {
				if o == "*" || o == origin {
					allowed = true
					break
				}
			}
			if !allowed {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
		}

		// 2. Authentication
		if cfg.AuthToken != "" {
			// Check Authorization header first
			auth := r.Header.Get("Authorization")
			token := strings.TrimPrefix(auth, "Bearer ")
			if token == "" {
				// Fallback to query param for easier EventSource usage
				token = r.URL.Query().Get("token")
			}

			if token != cfg.AuthToken {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // for nginx

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Optional retry directive
	if retry := r.URL.Query().Get("retry"); retry != "" {
		fmt.Fprintf(w, "retry: %s\n\n", retry)
		flusher.Flush()
	}

	// Subscribe to the hub
	ch, unsubscribe := hub.Subscribe(stream, 64)
	defer unsubscribe()

	notify := r.Context().Done()

	// Send a comment to keep some proxies happy
	fmt.Fprintf(w, ": connected to stream %s\n\n", stream)
	flusher.Flush()

	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if ev.ID != "" {
				fmt.Fprintf(w, "id: %s\n", ev.ID)
			}
			if ev.Event != "" {
				fmt.Fprintf(w, "event: %s\n", ev.Event)
			}
			// Per SSE spec, each line of data should be prefixed with "data: "
			// Split on newlines to support multi-line payloads.
			payload := string(ev.Data)
			for _, line := range splitLines(payload) {
				fmt.Fprintf(w, "data: %s\n", line)
			}
			fmt.Fprint(w, "\n")
			flusher.Flush()
		case <-time.After(30 * time.Second):
			// Heartbeat to avoid idle timeouts by proxies
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case <-notify:
			return
		}
	}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start <= len(s) {
		out = append(out, s[start:])
	}
	return out
}
