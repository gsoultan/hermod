package websocket

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// A sink built with zero timeouts must get defaults, not a sink that can never
// connect. The factory parses absent config keys with ParseDuration(""), which
// yields zero — and a zero connect timeout becomes context.WithTimeout(ctx, 0),
// a deadline that has already passed when the dial starts. Every connection
// attempt then fails instantly, so a WebSocket sink configured without explicit
// timeouts was dead on arrival, in the way that looks like a network problem
// rather than a configuration one.
func TestZeroTimeoutsMeanDefaultsNotADeadSink(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		// Drain frames until the client hangs up.
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	wsURL := "ws" + srv.URL[len("http"):]
	s := New(wsURL, nil, nil, 0, 0, 0, false, nil)
	defer s.Close()

	msg := &message.DefaultMessage{}
	msg.SetID("zero-timeouts")
	msg.SetPayload([]byte(`{"hello":"world"}`))

	if err := s.Write(t.Context(), msg); err != nil {
		t.Fatalf("a sink built with unset timeouts cannot write at all: %v", err)
	}
}

// A missed ack must not poison the connection for every later write.
//
// When the peer fails to ack in time, the read deadline expires and Write
// returns an error — correctly. But the connection was kept, and gorilla read
// errors are permanent: every retry reuses the dead connection, fails its ack
// read again, and the sink never re-dials. One slow ack turns into a write
// path that can never succeed again, while the peer looks perfectly healthy.
// The write-failure path already closes and drops the connection; the ack
// path did not.
func TestAMissedAckDoesNotPoisonTheConnection(t *testing.T) {
	var mu sync.Mutex
	conns := 0
	hold := make(chan struct{})
	defer close(hold)

	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		mu.Lock()
		conns++
		n := conns
		mu.Unlock()

		if n == 1 {
			// Read the frame and then never ack: the client's ack read must
			// time out. Hold the connection open so the failure is a silent
			// peer, not a closed one.
			_, _, _ = c.ReadMessage()
			<-hold
			return
		}
		// Later connections behave: read, ack correctly.
		for {
			_, data, err := c.ReadMessage()
			if err != nil {
				return
			}
			var env map[string]any
			if err := json.Unmarshal(data, &env); err != nil {
				return
			}
			id, _ := env["id"].(string)
			_ = c.WriteJSON(map[string]any{"ack": id, "ok": true})
		}
	}))
	defer srv.Close()

	wsURL := "ws" + srv.URL[len("http"):]
	s := New(wsURL, nil, nil, 2*time.Second, 300*time.Millisecond, 0, true, nil)
	defer s.Close()

	first := &message.DefaultMessage{}
	first.SetID("never-acked")
	first.SetPayload([]byte(`{"n":1}`))
	if err := s.Write(t.Context(), first); err == nil {
		t.Fatal("a write whose ack never came was reported as delivered")
	}

	second := &message.DefaultMessage{}
	second.SetID("acked")
	second.SetPayload([]byte(`{"n":2}`))
	if err := s.Write(t.Context(), second); err != nil {
		t.Fatalf("the write after a missed ack failed: %v\n"+
			"the sink kept the connection whose ack read already failed, and "+
			"gorilla read errors are permanent, so no later write can succeed "+
			"until something drops the connection", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if conns < 2 {
		t.Errorf("the second write went over the first connection (%d dial(s)); "+
			"after a failed ack read the sink must re-dial", conns)
	}
}

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

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := s.Write(ctx, msg); err != nil {
		t.Fatalf("sink write failed: %v", err)
	}
	_ = s.Close()
}
