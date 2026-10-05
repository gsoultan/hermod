package http

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
	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
	"github.com/gsoultan/hermod/pkg/comm/source/webhook"
	pkgengine "github.com/gsoultan/hermod/pkg/engine"
)

// The inbound WebSocket endpoint that answers.
//
// A caller connects to /api/ws/in/<path> and sends records as frames; each is
// dispatched to the webhook source whose path is that URL. The endpoint
// acknowledged a frame once it was queued, so the caller never learned what the
// workflow did with it. When that webhook source is set to respond
// synchronously, each frame is answered with the result instead, on the same
// connection, once the workflow has finished with it.
//
// The frame goes through everything between the caller and its answer: a real
// WebSocket connection, the handler, the webhook source, a real engine and its
// sink.

// webhookSources is a store that holds webhook sources and accepts request logs.
type webhookSources struct {
	storage.Storage
	sources []storage.Source
}

func (s webhookSources) ListSources(context.Context, storage.CommonFilter) ([]storage.Source, int, error) {
	return s.sources, len(s.sources), nil
}

func (webhookSources) CreateWebhookRequest(context.Context, storage.WebhookRequest) error { return nil }

type inSink struct {
	mu  sync.Mutex
	n   int
	err error
}

func (s *inSink) Write(context.Context, hermod.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	return s.err
}
func (*inSink) Ping(context.Context) error { return nil }
func (*inSink) Close() error               { return nil }

// connectIn opens a caller's connection to /api/ws/in/<path>, whose webhook
// source has the given config. With a sink, a real engine runs the workflow.
func connectIn(t *testing.T, path string, config map[string]string, sink *inSink) *websocket.Conn {
	t.Helper()
	fullPath := "/api/ws/in/" + path
	cfg := map[string]string{"path": fullPath}
	for k, v := range config {
		cfg[k] = v
	}
	h := &WSHandler{Handler: &handlers.Handler{
		Storage: webhookSources{sources: []storage.Source{{Type: "webhook", Config: cfg}}},
	}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/in/{path...}", h.HandleWSIn)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// The running workflow's source, as the engine holds it.
	src := webhook.NewWebhookSource(fullPath)
	if sink != nil {
		eng := pkgengine.NewEngine(src, []hermod.Sink{sink}, buffer.NewRingBuffer(8))
		eng.SetConfig(pkgengine.Config{MaxRetries: 1, RetryInterval: 10 * time.Millisecond, StatusInterval: time.Second})
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
	} else {
		t.Cleanup(func() { _ = src.Close() })
	}

	c, resp, err := websocket.DefaultDialer.Dial("ws"+srv.URL[len("http"):]+fullPath, nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// exchangeFrame writes one frame and returns the frame that answers it.
func exchangeFrame(t *testing.T, c *websocket.Conn, frame any) map[string]json.RawMessage {
	t.Helper()
	if err := c.WriteJSON(frame); err != nil {
		t.Fatalf("writing the frame: %v", err)
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, data, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("the frame was not answered: %v", err)
	}
	var answer map[string]json.RawMessage
	if err := json.Unmarshal(data, &answer); err != nil {
		t.Fatalf("the answer is not a JSON object: %v\n%s", err, data)
	}
	return answer
}

func text(answer map[string]json.RawMessage, key string) string {
	var s string
	_ = json.Unmarshal(answer[key], &s)
	return s
}

var record = map[string]any{"id": "req-1", "payload": map[string]any{"order_id": 7}}

// A source that says nothing about its response is acknowledged as it always
// was: existing callers read this frame.
func TestAnAsynchronousEndpointStillAcknowledges(t *testing.T) {
	c := connectIn(t, "async", nil, &inSink{})

	// A frame that is not a record envelope is the record, and is acked by id.
	answer := exchangeFrame(t, c, map[string]any{"order_id": 7})
	if string(answer["ok"]) != "true" || text(answer, "ack") == "" {
		t.Errorf("an asynchronous frame was answered %v", answer)
	}
	if _, has := answer["status"]; has {
		t.Errorf("an asynchronous frame was given a result: %v", answer)
	}
}

func TestASynchronousEndpointAnswersWithWhatWasDelivered(t *testing.T) {
	c := connectIn(t, "sync-ok", map[string]string{"response_mode": "sync"}, &inSink{})

	answer := exchangeFrame(t, c, record)
	if text(answer, "id") != "req-1" || text(answer, "status") != "delivered" || text(answer, "error") != "" {
		t.Errorf("the caller was told %v", answer)
	}
	if !strings.Contains(string(answer["record"]), `"order_id":7`) {
		t.Errorf("the answer does not carry the record: %s", answer["record"])
	}
}

func TestASynchronousEndpointAnswersWithWhyItFailed(t *testing.T) {
	sink := &inSink{err: errors.Join(hermod.ErrPermanent, errors.New("the destination rejected the row"))}
	c := connectIn(t, "sync-fail", map[string]string{"response_mode": "sync"}, sink)

	answer := exchangeFrame(t, c, record)
	if text(answer, "id") != "req-1" || text(answer, "status") != "failed" ||
		!strings.Contains(text(answer, "error"), "the destination rejected the row") {
		t.Errorf("the caller was told %v", answer)
	}
}

// The wait ran out and the record is still the workflow's.
func TestASynchronousEndpointSaysWhenItStoppedWaiting(t *testing.T) {
	c := connectIn(t, "sync-slow", map[string]string{"response_mode": "sync", "response_timeout": "50ms"}, nil)

	answer := exchangeFrame(t, c, record)
	if text(answer, "id") != "req-1" || text(answer, "status") != "pending" {
		t.Errorf("the caller was told %v", answer)
	}
}

// One connection carries many frames, and each is answered under its own id.
func TestEachFrameOnAConnectionIsAnsweredUnderItsOwnID(t *testing.T) {
	c := connectIn(t, "sync-many", map[string]string{"response_mode": "sync"}, &inSink{})

	for _, id := range []string{"a", "b", "c"} {
		answer := exchangeFrame(t, c, map[string]any{"id": id, "payload": map[string]any{"n": 1}})
		if text(answer, "id") != id || text(answer, "status") != "delivered" {
			t.Errorf("frame %q was answered %v", id, answer)
		}
	}
}

// The source is synchronous and its workflow is not running, so nothing holds
// the path. The frame is answered rejected under its id: a caller waiting for a
// result must not be left waiting for one that cannot come.
func TestASynchronousEndpointRejectsAFrameNothingWillRead(t *testing.T) {
	const fullPath = "/api/ws/in/sync-stopped"
	h := &WSHandler{Handler: &handlers.Handler{
		Storage: webhookSources{sources: []storage.Source{{
			Type:   "webhook",
			Config: map[string]string{"path": fullPath, "response_mode": "sync"},
		}}},
	}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/in/{path...}", h.HandleWSIn)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c, resp, err := websocket.DefaultDialer.Dial("ws"+srv.URL[len("http"):]+fullPath, nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	answer := exchangeFrame(t, c, record)
	if text(answer, "id") != "req-1" || text(answer, "status") != "rejected" || text(answer, "error") == "" {
		t.Errorf("the caller was told %v", answer)
	}
}
