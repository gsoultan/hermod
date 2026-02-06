package websocket

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

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

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
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
