package grpcsource

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
	"github.com/gsoultan/hermod/pkg/comm/source/grpc/proto"
	pkgengine "github.com/gsoultan/hermod/pkg/engine"
)

// Publish over a stream.
//
// A producer with many records opened one call per record. PublishStream is the
// same exchange over one long-lived stream: every record sent is answered with
// one response under that record's id — "dispatched", or for a source that
// responds synchronously, what the workflow did with it.
//
// These go through the real gRPC stack, the source, a real engine and its sink.

type publishStream = grpc.BidiStreamingClient[proto.PublishRequest, proto.PublishResponse]

// openStream opens a PublishStream to a server whose store holds one gRPC
// source with the given config. With a sink, a real engine runs its workflow;
// with running false, nothing holds the source's path.
func openStream(ctx context.Context, t *testing.T, path string, config map[string]string, sink *syncSink, running bool) publishStream {
	t.Helper()
	cfg := map[string]string{"path": path}
	for k, v := range config {
		cfg[k] = v
	}
	client := wireServer(t, sourcesStorage{sources: []storage.Source{{Type: "grpc", Config: cfg}}})

	if running {
		src := NewGrpcSource(path)
		if sink != nil {
			eng := pkgengine.NewEngine(src, []hermod.Sink{sink}, buffer.NewRingBuffer(8))
			eng.SetConfig(pkgengine.Config{MaxRetries: 1, RetryInterval: 10 * time.Millisecond, StatusInterval: time.Second})
			engCtx, cancel := context.WithCancel(t.Context())
			done := make(chan struct{})
			go func() {
				defer close(done)
				_ = eng.Start(engCtx)
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
	}

	stream, err := client.PublishStream(ctx)
	if err != nil {
		t.Fatalf("opening the stream: %v", err)
	}
	return stream
}

func sendRecord(t *testing.T, stream publishStream, path, id string) {
	t.Helper()
	if err := stream.Send(&proto.PublishRequest{Path: path, Id: id, Payload: []byte(`{"order_id":7}`)}); err != nil {
		t.Fatalf("sending %q: %v", id, err)
	}
}

func receive(t *testing.T, stream publishStream) *proto.PublishResponse {
	t.Helper()
	resp, err := stream.Recv()
	if err != nil {
		t.Fatalf("the record was not answered: %v", err)
	}
	return resp
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// Every record on the stream is answered under its own id, and the stream ends
// when the producer has finished and every record has been answered.
func TestEveryRecordOnAStreamIsAnswered(t *testing.T) {
	const path = "/grpc/stream/async"
	sink := &syncSink{}
	stream := openStream(testContext(t), t, path, nil, sink, true)

	for _, id := range []string{"a", "b", "c"} {
		sendRecord(t, stream, path, id)
		if resp := receive(t, stream); resp.Id != id || resp.Status != "dispatched" {
			t.Errorf("record %q was answered %q for %q", id, resp.Status, resp.Id)
		}
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatalf("closing the producer's side: %v", err)
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Errorf("the stream did not end cleanly after the producer finished: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for sink.count() < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if sink.count() != 3 {
		t.Errorf("three records were answered dispatched and the sink was written %d times", sink.count())
	}
}

func TestASynchronousStreamAnswersWithWhatWasDelivered(t *testing.T) {
	const path = "/grpc/stream/sync-ok"
	stream := openStream(testContext(t), t, path, map[string]string{"response_mode": "sync"}, &syncSink{}, true)

	sendRecord(t, stream, path, "rec-1")
	resp := receive(t, stream)
	if resp.Id != "rec-1" || resp.Status != "delivered" || resp.Error != "" {
		t.Errorf("the producer was told %q / %q for %q", resp.Status, resp.Error, resp.Id)
	}
	if !strings.Contains(string(resp.Record), `"order_id":7`) {
		t.Errorf("the answer does not carry the record: %s", resp.Record)
	}
}

func TestASynchronousStreamAnswersWithWhyItFailed(t *testing.T) {
	const path = "/grpc/stream/sync-fail"
	sink := &syncSink{err: errors.Join(hermod.ErrPermanent, errors.New("the destination rejected the row"))}
	stream := openStream(testContext(t), t, path, map[string]string{"response_mode": "sync"}, sink, true)

	sendRecord(t, stream, path, "rec-1")
	resp := receive(t, stream)
	if resp.Id != "rec-1" || resp.Status != "failed" || !strings.Contains(resp.Error, "the destination rejected the row") {
		t.Errorf("the producer was told %q / %q for %q", resp.Status, resp.Error, resp.Id)
	}
}

// The wait ran out and the record is still the workflow's.
func TestASynchronousStreamSaysWhenItStoppedWaiting(t *testing.T) {
	const path = "/grpc/stream/sync-slow"
	stream := openStream(testContext(t), t, path, map[string]string{"response_mode": "sync", "response_timeout": "50ms"}, nil, true)

	sendRecord(t, stream, path, "rec-1")
	if resp := receive(t, stream); resp.Id != "rec-1" || resp.Status != "pending" {
		t.Errorf("the producer was told %q for %q", resp.Status, resp.Id)
	}
}

// The producer has finished sending and its last records are still with the
// workflow. The stream must stay open until they are answered: ending it at
// the producer's close would throw away the answers it is waiting for.
func TestAStreamAnswersWhatWasSentBeforeTheProducerClosed(t *testing.T) {
	const path = "/grpc/stream/half-close"
	stream := openStream(testContext(t), t, path, map[string]string{"response_mode": "sync"}, &syncSink{}, true)

	sendRecord(t, stream, path, "rec-1")
	sendRecord(t, stream, path, "rec-2")
	if err := stream.CloseSend(); err != nil {
		t.Fatalf("closing the producer's side: %v", err)
	}

	answered := map[string]string{}
	for range 2 {
		resp := receive(t, stream)
		answered[resp.Id] = resp.Status
	}
	if answered["rec-1"] != "delivered" || answered["rec-2"] != "delivered" {
		t.Errorf("the records sent before the close were answered %v", answered)
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Errorf("the stream did not end cleanly once everything was answered: %v", err)
	}
}

// Nothing holds the path: the workflow is not running. The record is refused
// and said to be, and the stream stays open for the next one — a stopped
// workflow is not a reason to make the producer reconnect.
func TestARecordThatCannotBeQueuedIsRejectedAndTheStreamStaysOpen(t *testing.T) {
	const path = "/grpc/stream/stopped"
	stream := openStream(testContext(t), t, path, nil, nil, false)

	for _, id := range []string{"a", "b"} {
		sendRecord(t, stream, path, id)
		resp := receive(t, stream)
		if resp.Id != id || resp.Status != "rejected" || !strings.Contains(resp.Error, "no gRPC source registered") {
			t.Errorf("record %q was answered %q / %q for %q", id, resp.Status, resp.Error, resp.Id)
		}
	}
}

// The API key is checked on a stream as it is on a call. A record the key does
// not cover ends the stream with the error; it is not answered as a record.
func TestAStreamIsHeldToTheSourcesAPIKey(t *testing.T) {
	const path = "/grpc/stream/keyed"
	config := map[string]string{"api_key": "sesame"}

	noKey := openStream(testContext(t), t, path, config, &syncSink{}, true)
	sendRecord(t, noKey, path, "rec-1")
	if _, err := noKey.Recv(); err == nil || !strings.Contains(err.Error(), "invalid api key") {
		t.Errorf("a stream with no key was not refused for its key: %v", err)
	}

	keyed := metadata.AppendToOutgoingContext(testContext(t), "x-api-key", "sesame")
	withKey := openStream(keyed, t, path+"/2", config, &syncSink{}, true)
	sendRecord(t, withKey, path+"/2", "rec-1")
	if resp := receive(t, withKey); resp.Status != "dispatched" {
		t.Errorf("a stream with the right key was answered %q / %q", resp.Status, resp.Error)
	}
}

// rekeyableStorage holds one keyed gRPC source whose key can be changed.
type rekeyableStorage struct {
	storage.Storage
	mu   sync.Mutex
	path string
	key  string
}

func (s *rekeyableStorage) ListSources(context.Context, storage.CommonFilter) ([]storage.Source, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return []storage.Source{{Type: "grpc", Config: map[string]string{"path": s.path, "api_key": s.key}}}, 1, nil
}

func (s *rekeyableStorage) rekey(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.key = key
}

// A stream is long-lived, and its key is checked once per path and then
// trusted for a while rather than read from the store for every record. That
// trust has to run out: a key the operator has changed must stop working on a
// stream that was opened with the old one, not stay good until the producer
// happens to reconnect.
func TestAChangedKeyStopsWorkingOnAnOpenStream(t *testing.T) {
	old := streamGrantTTL
	streamGrantTTL = 20 * time.Millisecond
	t.Cleanup(func() { streamGrantTTL = old })

	const path = "/grpc/stream/rekeyed"
	store := &rekeyableStorage{path: path, key: "sesame"}
	client := wireServer(t, store)
	src := NewGrpcSource(path)
	t.Cleanup(func() { _ = src.Close() })

	ctx := metadata.AppendToOutgoingContext(testContext(t), "x-api-key", "sesame")
	stream, err := client.PublishStream(ctx)
	if err != nil {
		t.Fatalf("opening the stream: %v", err)
	}

	sendRecord(t, stream, path, "before")
	if resp := receive(t, stream); resp.Status != "dispatched" {
		t.Fatalf("the record sent with the current key was answered %q / %q", resp.Status, resp.Error)
	}

	store.rekey("a-new-key")
	time.Sleep(100 * time.Millisecond)

	sendRecord(t, stream, path, "after")
	if resp, err := stream.Recv(); err == nil || !strings.Contains(err.Error(), "invalid api key") {
		t.Errorf("a record sent after the key was changed was not refused for its key: %v / %v", resp, err)
	}
}
