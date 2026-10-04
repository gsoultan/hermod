package factory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
	pkgengine "github.com/gsoultan/hermod/pkg/engine"
)

type acceptingSink struct{}

func (acceptingSink) Write(context.Context, hermod.Message) error { return nil }
func (acceptingSink) Ping(context.Context) error                  { return nil }
func (acceptingSink) Close() error                                { return nil }

// A WebSocket source configured to respond synchronously answers the frames it
// reads. The source has its own tests for that, and they hand it the setting
// directly. This starts from the stored configuration — `response_mode: sync`
// — and builds the source the way a workflow does, so that a factory which
// stopped passing the setting on would fail here rather than leave an operator
// with a source that saves as synchronous and answers nothing.
func TestAWebSocketSourceBuiltFromItsConfigAnswersItsFrames(t *testing.T) {
	type frame struct {
		ID     string          `json:"id"`
		Status string          `json:"status"`
		Record json.RawMessage `json:"record"`
	}
	answered := make(chan *frame, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.WriteJSON(map[string]any{"id": "req-1", "payload": map[string]any{"order_id": 7}})
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
		var f frame
		if err := c.ReadJSON(&f); err != nil {
			answered <- nil
			return
		}
		answered <- &f
	}))
	t.Cleanup(srv.Close)

	src, err := CreateSource(SourceConfig{Type: "websocket", Config: map[string]string{
		"url":              "ws" + srv.URL[len("http"):],
		"response_mode":    "sync",
		"response_timeout": "5s",
	}})
	if err != nil {
		t.Fatalf("building the source from its config: %v", err)
	}

	eng := pkgengine.NewEngine(src, []hermod.Sink{acceptingSink{}}, buffer.NewRingBuffer(8))
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = eng.Start(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("the engine did not stop")
		}
	})

	select {
	case f := <-answered:
		if f == nil {
			t.Fatal("a source configured as synchronous wrote nothing back")
		}
		if f.ID != "req-1" || f.Status != "delivered" || !strings.Contains(string(f.Record), `"order_id":7`) {
			t.Errorf("the server was told %+v", f)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the server never finished its exchange")
	}
}
