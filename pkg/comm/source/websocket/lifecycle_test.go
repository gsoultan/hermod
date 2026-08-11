package websocket

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// ---------------------------------------------------------------------------
// Read-loop lifecycle.
//
// Read started the loop with a bare `go s.loop(ctx)` on the way past, and Close
// stopped it by closing the quit channel and then setting the field to nil.
// Neither is once-only, and the loop read that field without the lock.
//
// The race detector reports the unsynchronised read, which is the mildest of
// the three problems it points at:
//
//   - Read is called in a loop by the engine, once per message, and each call
//     spawned another loop. Every one of them dials the endpoint and reads from
//     the same connection, which gorilla/websocket does not allow.
//   - Close sets quit to nil after closing it. A loop that reads the field after
//     that selects on a nil channel, which never fires, falls through the
//     default case and keeps going — reconnecting forever to a source that has
//     been closed.
// ---------------------------------------------------------------------------

// echoServer accepts websocket connections and counts them, so a test can see
// how many readers a source actually opened.
func echoServer(t *testing.T, conns *atomic.Int64) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		conns.Add(1)
		defer c.Close()
		for {
			if err := c.WriteJSON(map[string]any{
				"id": "m", "op": "create", "table": "t", "payload": map[string]any{"a": 1},
			}); err != nil {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}))
}

func newTestSource(url string) *Source {
	return New(url, nil, nil, 2*time.Second, 2*time.Second, 0, 20*time.Millisecond, 100*time.Millisecond, 0)
}

// TestReadDoesNotStartALoopPerCall. The engine calls Read once per message, so a
// goroutine started on the way past is a goroutine per message — each one
// dialling the endpoint and reading the same connection concurrently.
func TestReadDoesNotStartALoopPerCall(t *testing.T) {
	var conns atomic.Int64
	srv := echoServer(t, &conns)
	defer srv.Close()

	src := newTestSource("ws" + srv.URL[len("http"):])
	defer src.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	before := runtime.NumGoroutine()
	for range 30 {
		if _, err := src.Read(ctx); err != nil {
			t.Fatalf("read: %v", err)
		}
	}
	time.Sleep(100 * time.Millisecond)

	if grew := runtime.NumGoroutine() - before; grew > 5 {
		t.Errorf("30 reads left %d extra goroutines behind; the read loop is started per call", grew)
	}
	if got := conns.Load(); got > 1 {
		t.Errorf("the source opened %d connections for one stream; "+
			"concurrent readers on one websocket are not supported", got)
	}
}

// TestCloseStopsTheLoop. Closing must actually stop it. Nil'ing the quit channel
// meant a loop could miss the signal entirely and keep reconnecting to a source
// the engine believes is shut.
func TestCloseStopsTheLoop(t *testing.T) {
	var conns atomic.Int64
	srv := echoServer(t, &conns)
	defer srv.Close()

	src := newTestSource("ws" + srv.URL[len("http"):])

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := src.Read(ctx); err != nil {
		t.Fatalf("read: %v", err)
	}

	if err := src.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reconnect backoff is 20ms, so a loop still running would dial several more
	// times in this window.
	settled := conns.Load()
	time.Sleep(300 * time.Millisecond)

	if got := conns.Load(); got > settled {
		t.Errorf("the source dialled %d more times after Close; the read loop did not stop",
			got-settled)
	}
}

// TestCloseIsIdempotent: shutdown paths double-close, and a second Close must
// not panic on an already-closed channel.
func TestCloseIsIdempotent(t *testing.T) {
	var conns atomic.Int64
	srv := echoServer(t, &conns)
	defer srv.Close()

	src := newTestSource("ws" + srv.URL[len("http"):])
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := src.Read(ctx); err != nil {
		t.Fatalf("read: %v", err)
	}

	if err := src.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := src.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

// TestCloseRacesRead is the report the detector produced, as a test: closing
// while reads are in flight is the ordinary shutdown case.
func TestCloseRacesRead(t *testing.T) {
	var conns atomic.Int64
	srv := echoServer(t, &conns)
	defer srv.Close()

	src := newTestSource("ws" + srv.URL[len("http"):])
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 10 {
				_, _ = src.Read(ctx)
			}
		}()
	}
	time.Sleep(20 * time.Millisecond)
	_ = src.Close()
	wg.Wait()
}
