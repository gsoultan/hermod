package grpcsource

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
	"github.com/gsoultan/hermod/pkg/comm/source/grpc/proto"
	pkgengine "github.com/gsoultan/hermod/pkg/engine"
)

// A Publish that waits for its answer.
//
// Publish returned "dispatched" as soon as the record was queued, so a
// producer never learned whether the workflow delivered it. A source set to
// respond synchronously returns when the workflow has finished with the record
// and says what happened to it.
//
// The record goes through everything between the producer and the answer: the
// real gRPC stack, the source, a real engine and its sink.

type syncSink struct {
	mu  sync.Mutex
	n   int
	err error
}

func (s *syncSink) Write(context.Context, hermod.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	return s.err
}
func (*syncSink) Ping(context.Context) error { return nil }
func (*syncSink) Close() error               { return nil }

func (s *syncSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

// publishTo sends one record to a source with the given config. With a sink, a
// real engine runs the source's workflow; without one, nothing ever reads.
func publishTo(t *testing.T, path string, config map[string]string, sink *syncSink) (*proto.PublishResponse, error) {
	t.Helper()
	cfg := map[string]string{"path": path}
	for k, v := range config {
		cfg[k] = v
	}
	client := wireServer(t, sourcesStorage{sources: []storage.Source{{Type: "grpc", Config: cfg}}})

	src := NewGrpcSource(path)
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

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	return client.Publish(ctx, &proto.PublishRequest{Path: path, Id: "rec-1", Payload: []byte(`{"order_id":7}`)})
}

// A source that says nothing about its response answers as it always has.
func TestPublishAnswersDispatchedUnlessToldToWait(t *testing.T) {
	resp, err := publishTo(t, "/grpc/sync/async", nil, &syncSink{})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if resp.Status != "dispatched" || resp.Id != "rec-1" {
		t.Errorf("an asynchronous publish answered %q for %q", resp.Status, resp.Id)
	}
	if len(resp.Record) != 0 || resp.Error != "" {
		t.Errorf("an asynchronous publish carried a result: %q / %s", resp.Error, resp.Record)
	}
}

func TestASynchronousPublishAnswersWithWhatWasDelivered(t *testing.T) {
	sink := &syncSink{}
	resp, err := publishTo(t, "/grpc/sync/ok", map[string]string{"response_mode": "sync"}, sink)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if resp.Status != "delivered" || resp.Id != "rec-1" || resp.Error != "" {
		t.Errorf("the producer was told %q / %q for %q", resp.Status, resp.Error, resp.Id)
	}
	if !strings.Contains(string(resp.Record), `"order_id":7`) {
		t.Errorf("the answer does not carry the record: %s", resp.Record)
	}
	if sink.count() != 1 {
		t.Errorf("the producer was told delivered and the sink was written %d times", sink.count())
	}
}

// The call itself succeeds: the producer reached Hermod and was answered. What
// the workflow did with the record is in the answer.
func TestASynchronousPublishAnswersWithWhyItFailed(t *testing.T) {
	sink := &syncSink{err: errors.Join(hermod.ErrPermanent, errors.New("the destination rejected the row"))}
	resp, err := publishTo(t, "/grpc/sync/fail", map[string]string{"response_mode": "sync"}, sink)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if resp.Status != "failed" || !strings.Contains(resp.Error, "the destination rejected the row") {
		t.Errorf("the producer was told %q / %q", resp.Status, resp.Error)
	}
}

// The wait ran out and the record is still the workflow's. That is not a
// failure, and a producer that retries one sends the record twice.
func TestASynchronousPublishSaysWhenItStoppedWaiting(t *testing.T) {
	started := time.Now()
	resp, err := publishTo(t, "/grpc/sync/slow", map[string]string{"response_mode": "sync", "response_timeout": "50ms"}, nil)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if resp.Status != "pending" || resp.Id != "rec-1" {
		t.Errorf("the producer was told %q for %q", resp.Status, resp.Id)
	}
	if waited := time.Since(started); waited > 3*time.Second {
		t.Errorf("the call was held for %v with a 50ms timeout", waited)
	}
}
