package registry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/factory"
)

// blockingSink is a test double whose Ping ignores the provided context and
// blocks until it is explicitly released. It is used to verify that
// Registry.TestSink enforces the context deadline regardless of whether the
// underlying sink honors the context.
type blockingSink struct {
	release chan struct{}
	closed  chan struct{}
}

func newBlockingSink() *blockingSink {
	return &blockingSink{
		release: make(chan struct{}),
		closed:  make(chan struct{}, 1),
	}
}

func (s *blockingSink) Write(_ context.Context, _ hermod.Message) error { return nil }

func (s *blockingSink) Ping(_ context.Context) error {
	// Intentionally ignore the context and block until released.
	<-s.release
	return nil
}

func (s *blockingSink) Close() error {
	select {
	case s.closed <- struct{}{}:
	default:
	}
	return nil
}

func TestTestSink_RespectsContextDeadline(t *testing.T) {
	r := NewRegistry(nil)

	snk := newBlockingSink()
	r.SetFactories(nil, func(factory.SinkConfig) (hermod.Sink, error) {
		return snk, nil
	})
	// Ensure the blocked goroutine is eventually released to avoid leaks.
	defer close(snk.release)

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := r.TestSink(ctx, factory.SinkConfig{Type: "blocking"})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected a timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
	if elapsed > time.Second {
		t.Fatalf("TestSink did not return promptly on deadline: took %v", elapsed)
	}
}

// nonBlockingSink honors the context and returns immediately on Ping.
type nonBlockingSink struct {
	pingErr error
}

func (s *nonBlockingSink) Write(_ context.Context, _ hermod.Message) error { return nil }
func (s *nonBlockingSink) Ping(_ context.Context) error                    { return s.pingErr }
func (s *nonBlockingSink) Close() error                                    { return nil }

func TestTestSink_ReturnsPingResult(t *testing.T) {
	tests := []struct {
		name    string
		pingErr error
		wantErr bool
	}{
		{"Success", nil, false},
		{"PingFailure", errors.New("connection refused"), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := NewRegistry(nil)
			r.SetFactories(nil, func(factory.SinkConfig) (hermod.Sink, error) {
				return &nonBlockingSink{pingErr: tc.pingErr}, nil
			})

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()

			err := r.TestSink(ctx, factory.SinkConfig{Type: "nonblocking"})
			if tc.wantErr && err == nil {
				t.Fatalf("expected an error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}
