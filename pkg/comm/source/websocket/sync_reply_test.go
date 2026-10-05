package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
	pkgengine "github.com/gsoultan/hermod/pkg/engine"
)

// A WebSocket source that answers what it reads.
//
// The source dials a server and reads frames. It never wrote one back, so the
// server at the other end could not learn what became of what it sent. Set to
// respond synchronously, the source writes one result frame per frame it read,
// on the same connection, once the workflow has finished with it.
//
// The frame goes through everything between the server and its answer: a real
// WebSocket connection, the source, a real engine and its sink.

type wsSink struct {
	mu  sync.Mutex
	n   int
	err error
}

func (s *wsSink) Write(context.Context, hermod.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	return s.err
}
func (*wsSink) Ping(context.Context) error { return nil }
func (*wsSink) Close() error               { return nil }

type resultFrame struct {
	ID     string          `json:"id"`
	Status string          `json:"status"`
	Error  string          `json:"error"`
	Record json.RawMessage `json:"record"`
}

// exchange serves one connection: it sends one frame and returns the first
// frame the source writes back within the wait, or nil if it writes none.
func exchange(t *testing.T, configure func(*Source), sink *wsSink, wait time.Duration) *resultFrame {
	t.Helper()
	answered := make(chan *resultFrame, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.WriteJSON(map[string]any{"id": "req-1", "payload": map[string]any{"order_id": 7}})
		_ = c.SetReadDeadline(time.Now().Add(wait))
		var f resultFrame
		if err := c.ReadJSON(&f); err != nil {
			answered <- nil
			return
		}
		answered <- &f
	}))
	t.Cleanup(srv.Close)

	src := New("ws"+srv.URL[len("http"):], nil, nil, 0, 0, 0, 0, 0, 0)
	if configure != nil {
		configure(src)
	}

	ctx, cancel := context.WithCancel(t.Context())
	if sink != nil {
		eng := pkgengine.NewEngine(src, []hermod.Sink{sink}, buffer.NewRingBuffer(8))
		eng.SetConfig(pkgengine.Config{MaxRetries: 1, RetryInterval: 10 * time.Millisecond, StatusInterval: time.Second})
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
	} else {
		// Nothing runs the workflow: the frame is read and never finished.
		go func() { _, _ = src.Read(ctx) }()
		t.Cleanup(func() {
			cancel()
			_ = src.Close()
		})
	}

	select {
	case f := <-answered:
		return f
	case <-time.After(wait + 5*time.Second):
		t.Fatal("the server never finished its exchange")
		return nil
	}
}

// A source that says nothing about its response writes nothing back, as before.
func TestTheSourceWritesNothingBackUnlessToldTo(t *testing.T) {
	if f := exchange(t, nil, &wsSink{}, 400*time.Millisecond); f != nil {
		t.Errorf("an asynchronous source wrote a frame back: %+v", f)
	}
}

func TestASynchronousSourceAnswersTheFrameItRead(t *testing.T) {
	sink := &wsSink{}
	f := exchange(t, func(s *Source) { s.SetResponse(true, 5*time.Second) }, sink, 5*time.Second)
	if f == nil {
		t.Fatal("a synchronous source wrote nothing back")
	}
	if f.ID != "req-1" || f.Status != "delivered" || f.Error != "" {
		t.Errorf("the server was told %+v", f)
	}
	if !strings.Contains(string(f.Record), `"order_id":7`) {
		t.Errorf("the answer does not carry the record: %s", f.Record)
	}
}

func TestASynchronousSourceAnswersWithWhyItFailed(t *testing.T) {
	sink := &wsSink{err: errors.Join(hermod.ErrPermanent, errors.New("the destination rejected the row"))}
	f := exchange(t, func(s *Source) { s.SetResponse(true, 5*time.Second) }, sink, 5*time.Second)
	if f == nil {
		t.Fatal("a synchronous source wrote nothing back")
	}
	if f.ID != "req-1" || f.Status != "failed" || !strings.Contains(f.Error, "the destination rejected the row") {
		t.Errorf("the server was told %+v", f)
	}
}

// The wait ran out and the frame is still the workflow's.
func TestASynchronousSourceSaysWhenItStoppedWaiting(t *testing.T) {
	f := exchange(t, func(s *Source) { s.SetResponse(true, 50*time.Millisecond) }, nil, 3*time.Second)
	if f == nil {
		t.Fatal("a synchronous source wrote nothing back when its wait ran out")
	}
	if f.ID != "req-1" || f.Status != "pending" {
		t.Errorf("the server was told %+v", f)
	}
}
