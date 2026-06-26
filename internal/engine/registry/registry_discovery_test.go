package registry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/factory"
)

// panicSource is a source whose Ping panics, simulating a database driver or
// network client that dereferences a nil pointer / fails an unchecked type
// assertion while a user is testing a misconfigured source connection.
type panicSource struct{}

func (s *panicSource) Read(ctx context.Context) (hermod.Message, error)  { return nil, nil }
func (s *panicSource) Ack(ctx context.Context, msg hermod.Message) error { return nil }
func (s *panicSource) Ping(ctx context.Context) error {
	panic("boom: simulated driver panic during connectivity test")
}
func (s *panicSource) Close() error { return nil }

// TestTestSource_PanicContained is the regression test for the production 520:
// TestSource runs createSource+Ping inside a goroutine spawned by
// runWithContext. When that goroutine panicked there was no recover(), so the
// panic propagated to the goroutine's top and crashed the whole process (which
// also hosts the API server), surfacing to clients (via Cloudflare) as a 520.
//
// With the fix the panic is contained: TestSource returns an error tagged with
// errOperationPanicked and the process keeps running.
func TestTestSource_PanicContained(t *testing.T) {
	r := NewRegistry(nil)
	r.SetFactories(func(cfg factory.SourceConfig) (hermod.Source, error) {
		return &panicSource{}, nil
	}, nil)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	err := r.TestSource(ctx, factory.SourceConfig{Type: "panic"})
	if err == nil {
		t.Fatal("expected an error from a panicking source, got nil")
	}
	if !errors.Is(err, errOperationPanicked) {
		t.Fatalf("expected error to wrap errOperationPanicked, got %v", err)
	}
}

// nilSource is a factory result that returns a nil source with a nil error,
// which would previously cause a nil-pointer dereference (panic) on the
// deferred Close()/Ping() in TestSource.
func TestTestSource_NilSourceGuard(t *testing.T) {
	r := NewRegistry(nil)
	r.SetFactories(func(cfg factory.SourceConfig) (hermod.Source, error) {
		return nil, nil
	}, nil)

	err := r.TestSource(t.Context(), factory.SourceConfig{Type: "nil"})
	if err == nil {
		t.Fatal("expected an error for a nil source, got nil")
	}
}
