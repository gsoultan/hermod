package websocket

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// A source built with zero timeouts must get a usable connect timeout, not a
// context whose deadline has already passed. The factory parses absent config
// keys into zero, and the dial wraps ctx in WithTimeout(ctx, 0) — expired
// before the dial starts — so a WebSocket source configured without an
// explicit connect_timeout could never connect at all. The read timeout is
// already guarded (> 0 means "set a deadline", zero means none); the connect
// timeout had no such guard. The sink had the identical defect.
func TestZeroTimeoutsMeanDefaultsNotADeadSource(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.WriteJSON(map[string]any{"id": "msg-1", "payload": map[string]any{"n": 1}})
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()

	wsURL := "ws" + srv.URL[len("http"):]
	src := New(wsURL, nil, nil, 0, 0, 0, 0, 0, 0)
	defer src.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	m, err := src.Read(ctx)
	if err != nil {
		t.Fatalf("a source built with unset timeouts cannot read at all: %v", err)
	}
	if m == nil {
		t.Fatal("nil message")
	}
}

func TestWebSocketSource_Read(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("upgrade error: %v", err)
			return
		}
		defer c.Close()

		env := map[string]any{
			"id":      "msg-1",
			"op":      "create",
			"table":   "users",
			"schema":  "public",
			"payload": map[string]any{"hello": "world"},
		}
		_ = c.WriteJSON(env)
		time.Sleep(50 * time.Millisecond)
	}))
	defer srv.Close()

	wsURL := "ws" + srv.URL[len("http"):]
	src := New(wsURL, nil, nil, 5*time.Second, 5*time.Second, 0, time.Second, 5*time.Second, 0)

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	m, err := src.Read(ctx)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if m == nil {
		t.Fatal("nil message")
	}
	if len(m.Payload()) == 0 {
		t.Fatal("expected payload")
	}
	_ = src.Close()
}
