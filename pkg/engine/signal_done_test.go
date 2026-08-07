package engine

import (
	"errors"
	"testing"
	"time"
)

// pendingMessage.done has capacity 1 and thirteen sites send on it. Two of them
// can legitimately conclude the same message — the sink writer finishing it and
// the drop_oldest evictor discarding it — and the second raw send then blocks
// forever, because nobody reads a channel that already holds the first outcome.
//
// A blocked writer goroutine stops draining its channel, which backs up into the
// ingestion buffer, which blocks the source's dispatch, which stops replication
// acknowledgement. That is the shape of the wedge that survived a sink coming
// back healthy: 0 of 3000 messages delivered, recoverable only by restarting the
// workflow.
//
// Only the first outcome is meaningful, so a second signal must be dropped
// rather than block.
func TestSignalDoneNeverBlocks(t *testing.T) {
	pm := &pendingMessage{done: make(chan error, 1)}

	done := make(chan struct{})
	go func() {
		defer close(done)
		signalDone(pm, errors.New("first"))
		signalDone(pm, errors.New("second")) // would block forever unguarded
		signalDone(pm, nil)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("signalDone blocked; the writer goroutine would wedge the pipeline")
	}

	// The first outcome is the one that survives.
	select {
	case err := <-pm.done:
		if err == nil || err.Error() != "first" {
			t.Fatalf("got %v, want the first outcome", err)
		}
	default:
		t.Fatal("no outcome was delivered at all")
	}
}

// A reader waiting on the message still gets its result.
func TestSignalDoneDeliversToWaiter(t *testing.T) {
	pm := &pendingMessage{done: make(chan error, 1)}
	want := errors.New("sink write failed")

	go signalDone(pm, want)

	select {
	case got := <-pm.done:
		if !errors.Is(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waiter never received the outcome")
	}
}
