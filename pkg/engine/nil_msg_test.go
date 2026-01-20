package engine

import (
	"context"
	"testing"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/buffer"
)

type nilMsgSource struct {
	hermod.Source
	yielded bool
}

func (s *nilMsgSource) Read(ctx context.Context) (hermod.Message, error) {
	if !s.yielded {
		s.yielded = true
		return nil, nil // Yield a nil message
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (s *nilMsgSource) Ping(ctx context.Context) error                    { return nil }
func (s *nilMsgSource) Ack(ctx context.Context, msg hermod.Message) error { return nil }
func (s *nilMsgSource) Close() error                                      { return nil }

type nilMsgSink struct {
	hermod.Sink
}

func (s *nilMsgSink) Write(ctx context.Context, msg hermod.Message) error {
	// If it reached here with nil and didn't panic, it's good, but we already added nil check in Write
	return nil
}
func (s *nilMsgSink) Ping(ctx context.Context) error { return nil }

func TestNilMessageHandling(t *testing.T) {
	src := &nilMsgSource{}
	snk := &nilMsgSink{}
	buf := buffer.NewRingBuffer(10)

	eng := NewEngine(src, []hermod.Sink{snk}, buf)
	eng.SetIDs("workflow-1", "src-1", []string{"snk-1"})
	eng.SetLogger(NewDefaultLogger())

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Should not panic
	err := eng.Start(ctx)
	if err != nil && err != context.DeadlineExceeded {
		t.Fatalf("Engine stopped with error: %v", err)
	}
}
