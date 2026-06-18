package registry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/factory"
)

// blockingDiscoverSink is a test double whose discovery methods ignore the
// provided context and block until explicitly released. It is used to verify
// that the registry's sink discovery enforces the context deadline regardless
// of whether the underlying sink/driver honors the context (the root cause of
// the upstream 524 gateway timeout on /api/sinks/discover/tables).
type blockingDiscoverSink struct {
	release chan struct{}
}

func newBlockingDiscoverSink() *blockingDiscoverSink {
	return &blockingDiscoverSink{release: make(chan struct{})}
}

func (s *blockingDiscoverSink) Write(_ context.Context, _ hermod.Message) error { return nil }
func (s *blockingDiscoverSink) Ping(_ context.Context) error                    { return nil }
func (s *blockingDiscoverSink) Close() error                                    { return nil }

func (s *blockingDiscoverSink) DiscoverDatabases(_ context.Context) ([]string, error) {
	<-s.release
	return nil, nil
}

func (s *blockingDiscoverSink) DiscoverTables(_ context.Context) ([]string, error) {
	// Intentionally ignore the context and block until released.
	<-s.release
	return []string{"public.users"}, nil
}

func (s *blockingDiscoverSink) DiscoverColumns(_ context.Context, _ string) ([]hermod.ColumnInfo, error) {
	<-s.release
	return nil, nil
}

func TestDiscoverSinkTables_RespectsContextDeadline(t *testing.T) {
	r := NewRegistry(nil)

	snk := newBlockingDiscoverSink()
	r.SetFactories(nil, func(factory.SinkConfig) (hermod.Sink, error) {
		return snk, nil
	})
	// Ensure the blocked goroutine is eventually released to avoid leaks.
	defer close(snk.release)

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := r.DiscoverSinkTables(ctx, factory.SinkConfig{Type: "blocking"})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected a timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
	if elapsed > time.Second {
		t.Fatalf("DiscoverSinkTables did not return promptly on deadline: took %v", elapsed)
	}
}

// discoverSink honors the context and returns its tables immediately.
type discoverSink struct {
	tables []string
}

func (s *discoverSink) Write(_ context.Context, _ hermod.Message) error { return nil }
func (s *discoverSink) Ping(_ context.Context) error                    { return nil }
func (s *discoverSink) Close() error                                    { return nil }
func (s *discoverSink) DiscoverDatabases(_ context.Context) ([]string, error) {
	return nil, nil
}
func (s *discoverSink) DiscoverTables(_ context.Context) ([]string, error) {
	return s.tables, nil
}
func (s *discoverSink) DiscoverColumns(_ context.Context, _ string) ([]hermod.ColumnInfo, error) {
	return nil, nil
}

func TestDiscoverSinkTables_ReturnsResult(t *testing.T) {
	r := NewRegistry(nil)

	want := []string{"public.users", "public.orders"}
	r.SetFactories(nil, func(factory.SinkConfig) (hermod.Sink, error) {
		return &discoverSink{tables: want}, nil
	})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	got, err := r.DiscoverSinkTables(ctx, factory.SinkConfig{Type: "discover"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d tables, got %d (%v)", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("table[%d] = %q; want %q", i, got[i], want[i])
		}
	}
}
