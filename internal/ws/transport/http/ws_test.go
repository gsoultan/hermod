package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/user/hermod/internal/engine/registry"
)

// waitForStatusSubscribers polls until the registry reports the expected number
// of active status subscribers or the deadline elapses.
func waitForStatusSubscribers(t *testing.T, reg *registry.Registry, want int, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if reg.StatusSubscriberCount() == want {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return reg.StatusSubscriberCount() == want
}

// TestHandleStatusWS_ReleasesSubscriberOnClientDisconnect verifies that the
// status WebSocket handler observes the client closing the connection and
// releases its registry subscription promptly, rather than leaking it until a
// later failed write. This protects the long-lived /api/ws/status stream from
// accumulating dead subscribers/goroutines behind reverse proxies.
func TestHandleStatusWS_ReleasesSubscriberOnClientDisconnect(t *testing.T) {
	reg := registry.NewRegistry(nil)
	defer reg.Close()

	h := &Handler{Registry: reg}

	srv := httptest.NewServer(http.HandlerFunc(h.HandleStatusWS))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws/status"

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial status websocket: %v", err)
	}

	if !waitForStatusSubscribers(t, reg, 1, 2*time.Second) {
		_ = conn.Close()
		t.Fatalf("expected 1 status subscriber after connect, got %d", reg.StatusSubscriberCount())
	}

	// Close the client connection; the server must detect this and unsubscribe.
	if err := conn.Close(); err != nil {
		t.Fatalf("failed to close client connection: %v", err)
	}

	if !waitForStatusSubscribers(t, reg, 0, 2*time.Second) {
		t.Fatalf("expected status subscriber to be released after client disconnect, got %d", reg.StatusSubscriberCount())
	}
}
