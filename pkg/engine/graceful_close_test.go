package engine

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/comm/buffer"
)

// closeTrackingSource records whether Close was invoked. It blocks in Read
// until the context is cancelled, mimicking a streaming source whose Read
// returns only on shutdown.
type closeTrackingSource struct {
	closed atomic.Bool
}

func (s *closeTrackingSource) Read(ctx context.Context) (hermod.Message, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (s *closeTrackingSource) Ack(ctx context.Context, msg hermod.Message) error { return nil }
func (s *closeTrackingSource) Ping(ctx context.Context) error                    { return nil }

func (s *closeTrackingSource) Close() error {
	s.closed.Store(true)
	return nil
}

// TestGracefulStopClosesSource guards against a regression where the source was
// closed only by HardStop. On a normal stop/restart the source must be closed
// so streaming resources (e.g. a Postgres replication slot) are released and do
// not leak into the next source instance.
func TestGracefulStopClosesSource(t *testing.T) {
	src := &closeTrackingSource{}
	sink := &mockSink{received: make(chan hermod.Message, 1)}
	rb := buffer.NewRingBuffer(10)

	eng := NewEngine(src, []hermod.Sink{sink}, rb)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		_ = eng.Start(ctx)
		close(done)
	}()

	// Let the engine reach its running loops, then request a graceful stop.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("engine did not stop within timeout")
	}

	if !src.closed.Load() {
		t.Fatal("expected source.Close to be called on graceful stop, but it was not")
	}
}
