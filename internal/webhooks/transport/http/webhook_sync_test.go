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

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
	"github.com/gsoultan/hermod/pkg/comm/source/webhook"
	pkgengine "github.com/gsoultan/hermod/pkg/engine"
)

// A webhook that waits for its answer.
//
// The endpoint answered 202 "dispatched" as soon as the request was queued, so
// a caller never learned whether the workflow delivered what it sent. A source
// set to respond synchronously holds the request until the workflow has
// finished with it and answers with what happened: the status, the error if it
// failed, and the record as the workflow left it.
//
// These run the request through everything between the caller and the answer:
// the handler, the webhook source, a real engine and its sink.

// sourcesOnly is a store that holds webhook sources and accepts request logs.
type sourcesOnly struct {
	storage.Storage
	sources []storage.Source
}

func (s sourcesOnly) ListSources(context.Context, storage.CommonFilter) ([]storage.Source, int, error) {
	return s.sources, len(s.sources), nil
}

func (sourcesOnly) CreateWebhookRequest(context.Context, storage.WebhookRequest) error { return nil }

type recordingSink struct {
	mu  sync.Mutex
	n   int
	err error
}

func (s *recordingSink) Write(context.Context, hermod.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	return s.err
}
func (*recordingSink) Ping(context.Context) error { return nil }
func (*recordingSink) Close() error               { return nil }

func (s *recordingSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

// post sends one webhook to a handler whose store holds a source with the
// given config. With a sink, a real engine runs the source's workflow.
func post(t *testing.T, path string, config map[string]string, sink *recordingSink) *httptest.ResponseRecorder {
	t.Helper()
	fullPath := "/api/webhooks/" + path
	cfg := map[string]string{"path": fullPath}
	for k, v := range config {
		cfg[k] = v
	}
	h := NewWebhookHandler(&handlers.Handler{
		Storage: sourcesOnly{sources: []storage.Source{{Type: "webhook", Config: cfg}}},
	})
	mux := http.NewServeMux()
	h.RegisterWebhookRoutes(mux)

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

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, fullPath, strings.NewReader(`{"order_id":7}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	return rec
}

type syncReply struct {
	ID     string          `json:"id"`
	Status string          `json:"status"`
	Error  string          `json:"error"`
	Record json.RawMessage `json:"record"`
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) syncReply {
	t.Helper()
	var r syncReply
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatalf("the response is not JSON: %v\n%s", err, rec.Body.String())
	}
	return r
}

// A source that says nothing about its response answers as it always has.
func TestAWebhookAnswersDispatchedUnlessToldToWait(t *testing.T) {
	sink := &recordingSink{}
	rec := post(t, "async", nil, sink)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status %d, want 202: %s", rec.Code, rec.Body.String())
	}
	if r := decode(t, rec); r.Status != "dispatched" || r.ID == "" {
		t.Errorf("an asynchronous webhook answered %+v", r)
	}
}

func TestASynchronousWebhookAnswersWithWhatWasDelivered(t *testing.T) {
	sink := &recordingSink{}
	rec := post(t, "sync-ok", map[string]string{"response_mode": "sync"}, sink)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body.String())
	}
	r := decode(t, rec)
	if r.Status != "delivered" || r.ID == "" || r.Error != "" {
		t.Errorf("the caller was told %+v", r)
	}
	if !strings.Contains(string(r.Record), `"order_id":7`) {
		t.Errorf("the answer does not carry the record: %s", r.Record)
	}
	if sink.count() != 1 {
		t.Errorf("the caller was told delivered and the sink was written %d times", sink.count())
	}
}

func TestASynchronousWebhookAnswersWithWhyItFailed(t *testing.T) {
	sink := &recordingSink{err: errors.Join(hermod.ErrPermanent, errors.New("the destination rejected the row"))}
	rec := post(t, "sync-fail", map[string]string{"response_mode": "sync"}, sink)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d, want 502: %s", rec.Code, rec.Body.String())
	}
	r := decode(t, rec)
	if r.Status != "failed" || !strings.Contains(r.Error, "the destination rejected the row") {
		t.Errorf("the caller was told %+v", r)
	}
}

// The workflow is not finished when the caller's wait runs out. The request
// was accepted and is still being processed, and the answer says so, with the
// id to follow it by. It is not reported as a failure: a caller that retries
// one sends the record twice.
func TestASynchronousWebhookSaysWhenItStoppedWaiting(t *testing.T) {
	started := time.Now()
	rec := post(t, "sync-slow", map[string]string{"response_mode": "sync", "response_timeout": "50ms"}, nil)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status %d, want 202: %s", rec.Code, rec.Body.String())
	}
	if r := decode(t, rec); r.Status != "pending" || r.ID == "" {
		t.Errorf("the caller was told %+v", r)
	}
	if waited := time.Since(started); waited > 3*time.Second {
		t.Errorf("the request was held for %v with a 50ms timeout", waited)
	}
}

// A timeout that does not parse must not turn into no timeout at all, or into
// an asynchronous answer the operator did not ask for.
func TestAnUnreadableTimeoutFallsBackToTheDefault(t *testing.T) {
	sink := &recordingSink{}
	rec := post(t, "sync-bad-timeout", map[string]string{"response_mode": "sync", "response_timeout": "soon"}, sink)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if r := decode(t, rec); r.Status != "delivered" {
		t.Errorf("the caller was told %+v", r)
	}
}
