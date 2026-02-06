package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/user/hermod/internal/sse"
)

// handleSSEStream streams events from the in-process SSE hub.
// Query params:
//   - stream: name of the stream (default: "default")
//   - retry: reconnection delay in ms (optional)
func (s *Server) handleSSEStream(w http.ResponseWriter, r *http.Request) {
	stream := r.URL.Query().Get("stream")
	if stream == "" {
		stream = "default"
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

	// Subscribe
	ch, unsubscribe := sse.GetHub().Subscribe(stream, 64)
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
