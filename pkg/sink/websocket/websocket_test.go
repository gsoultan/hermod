package websocket

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/user/hermod/pkg/message"
)

func TestWebSocketSink_Write_WithAck(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("upgrade error: %v", err)
			return
		}
		defer c.Close()
		// Read one message then ACK
		_, data, err := c.ReadMessage()
		if err != nil {
			t.Errorf("server read: %v", err)
			return
		}
		var env map[string]any
		if err := json.Unmarshal(data, &env); err != nil {
			t.Errorf("unmarshal: %v", err)
			return
		}
		id, _ := env["id"].(string)
		ack := map[string]any{"ack": id, "ok": true}
		_ = c.WriteJSON(ack)
	}))
	defer srv.Close()

	wsURL := "ws" + srv.URL[len("http"):]
	s := New(wsURL, nil, nil, 5*time.Second, 5*time.Second, 0, true, nil)

	msg := &message.DefaultMessage{}
	msg.SetID("abc-123")
	msg.SetPayload([]byte(`{"hello":"world"}`))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Write(ctx, msg); err != nil {
		t.Fatalf("sink write failed: %v", err)
	}
	_ = s.Close()
}
