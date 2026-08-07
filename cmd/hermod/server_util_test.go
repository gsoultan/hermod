package main

import (
	"errors"
	"net"
	"net/http"
	"testing"
	"time"
)

// A listener that cannot bind used to be logged and then forgotten: the
// goroutine ended, but the process stayed blocked on <-ctx.Done() forever,
// holding no sockets at all. That leaves a process that is "running" while
// serving nothing — invisible to a port-based health check or cleanup sweep,
// and in Kubernetes a pod that never becomes useful and never restarts.
func TestStartServersAsyncReportsBindFailure(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not occupy a port: %v", err)
	}
	defer func() { _ = occupied.Close() }()

	blocked := make(chan struct{})
	defer close(blocked)

	fatal := startServersAsync(
		func() error { return (&http.Server{Addr: occupied.Addr().String()}).ListenAndServe() },
		func() error { <-blocked; return nil },
	)

	select {
	case err := <-fatal:
		if err == nil {
			t.Fatal("expected a bind error, got nil")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("bind failure was never reported: the process would stay alive with no listener")
	}
}

// The counterpart: a normal shutdown closes the listeners on purpose, and must
// not be mistaken for a failure. http.Server reports that as ErrServerClosed
// and gRPC's Serve returns nil after GracefulStop.
func TestStartServersAsyncIgnoresGracefulClose(t *testing.T) {
	fatal := startServersAsync(
		func() error { return http.ErrServerClosed },
		func() error { return nil },
	)

	select {
	case err := <-fatal:
		t.Fatalf("graceful shutdown reported as fatal: %v", err)
	case <-time.After(250 * time.Millisecond):
	}
}

// The error must name which listener failed, because the two ports are
// configured separately and "address already in use" alone does not say which
// one to change.
func TestStartServersAsyncNamesTheFailingListener(t *testing.T) {
	boom := errors.New("bind: address already in use")

	blocked := make(chan struct{})
	defer close(blocked)

	fatal := startServersAsync(
		func() error { <-blocked; return nil },
		func() error { return boom },
	)

	select {
	case err := <-fatal:
		if !errors.Is(err, boom) {
			t.Fatalf("original error not wrapped: %v", err)
		}
		if got := err.Error(); got == boom.Error() {
			t.Fatalf("error does not say which listener failed: %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("gRPC bind failure was never reported")
	}
}
