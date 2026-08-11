package webhook

import (
	"testing"

	"github.com/user/hermod/pkg/comm/message"
)

// ---------------------------------------------------------------------------
// Registry ownership during failover.
//
// The registry is keyed by path, and Unregister deleted whatever was at that
// key. That is fine while one worker owns a path and only becomes wrong at the
// moment the platform is designed to survive: a handover.
//
// When a worker loses its lease, the worker taking over registers the same path
// before the outgoing one has finished tearing down — nothing orders those two.
// The outgoing Close then closed and deleted the *incoming* worker's channel,
// leaving it reading from a closed channel that no longer appears in the
// registry. The workflow reports itself running and never receives another
// message.
//
// It surfaced as a flaky end-to-end failover test, which is the mild symptom.
// In production it is a workflow that silently stops delivering after a
// failover, with a green status next to it.
// ---------------------------------------------------------------------------

func TestTeardownDoesNotRemoveASupersedingRegistration(t *testing.T) {
	const path = "/failover"

	outgoing := Register(path)
	incoming := Register(path) // the worker taking the lease over

	if outgoing == incoming {
		t.Fatal("the second registration reused the first channel; " +
			"two workers would consume from the same source")
	}

	// The outgoing worker finishes tearing down after the handover.
	Unregister(path, outgoing)

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetID("after-failover")

	if err := Dispatch(path, msg); err != nil {
		t.Fatalf("dispatch after failover: %v; the outgoing worker's teardown removed "+
			"the incoming worker's registration, so the workflow is running but deaf", err)
	}

	select {
	case got := <-incoming:
		if got.ID() != "after-failover" {
			t.Errorf("the incoming worker received %q", got.ID())
		}
	default:
		t.Error("the message did not reach the worker that now owns the path")
	}

	Unregister(path, incoming)
}

// TestTeardownStillRemovesItsOwnRegistration: the ownership check must not stop
// a worker cleaning up after itself, or every path leaks its channel and a
// later Dispatch delivers into a source nobody is reading.
func TestTeardownStillRemovesItsOwnRegistration(t *testing.T) {
	const path = "/normal"

	ch := Register(path)
	Unregister(path, ch)

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)

	if err := Dispatch(path, msg); err == nil {
		t.Error("the path is still registered after its only owner closed")
	}
}

// TestClosingTheSourceReleasesItsPath exercises the same thing through the
// Source, which is what the engine actually calls.
func TestClosingTheSourceReleasesItsPath(t *testing.T) {
	const path = "/via-source"

	s := NewWebhookSource(path)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)

	if err := Dispatch(path, msg); err == nil {
		t.Error("closing the source left its path registered")
	}
}

// TestAClosedSourceDoesNotTakeOverAnotherOne is the Source-level version of the
// failover case: the outgoing source's Close must leave the incoming one alone.
func TestAClosedSourceDoesNotTakeOverAnotherOne(t *testing.T) {
	const path = "/handover"

	outgoing := NewWebhookSource(path)
	incoming := NewWebhookSource(path)

	if err := outgoing.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetID("x")

	if err := Dispatch(path, msg); err != nil {
		t.Fatalf("dispatch after handover: %v", err)
	}
	select {
	case <-incoming.ch:
	default:
		t.Error("the surviving source did not receive the message")
	}

	_ = incoming.Close()
}
