package form

import (
	"testing"

	"github.com/gsoultan/Hermod/pkg/comm/message"
)

// ---------------------------------------------------------------------------
// Registry ownership during failover.
//
// The registry is keyed by path and Unregister deleted whatever sat at that key,
// while Register handed a second caller the *same* channel as the first. That
// combination is only wrong at the moment the platform exists to survive.
//
// When a workflow moves between workers, the one taking the lease over registers
// the same path before the outgoing one has finished tearing down — nothing
// orders those two. The incoming source was handed the outgoing one's channel,
// and the outgoing Close then closed it. The new owner was left reading from a
// closed channel: the workflow reports itself running and never receives another
// message.
//
// The same defect wedged a real failover through the webhook registry, where it
// surfaced as an intermittent end-to-end failure. These three are structurally
// identical and were fixed alongside it rather than left to be found the hard way.
// ---------------------------------------------------------------------------

func TestTeardownDoesNotRemoveASupersedingRegistration(t *testing.T) {
	const path = "/form/failover"

	outgoing := Register(path)
	incoming := Register(path) // the worker taking the lease over

	if outgoing == incoming {
		t.Fatal("the second registration was handed the first channel; " +
			"the outgoing worker's teardown then closes it under the incoming one")
	}

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
// a source cleaning up after itself, or every path leaks its channel.
func TestTeardownStillRemovesItsOwnRegistration(t *testing.T) {
	const path = "/form/failover/solo"

	ch := Register(path)
	Unregister(path, ch)

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)

	if err := Dispatch(path, msg); err == nil {
		t.Error("the path is still registered after its only owner closed")
	}
}

// TestClosingTheSourceReleasesItsPath goes through the Source, which is what the
// engine actually calls.
func TestClosingTheSourceReleasesItsPath(t *testing.T) {
	const path = "/form/failover/via-source"

	s := NewFormSource(path, nil)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)

	if err := Dispatch(path, msg); err == nil {
		t.Error("closing the source left its path registered")
	}
}

// TestAClosedSourceDoesNotTakeOverAnotherOne is the Source-level failover case.
func TestAClosedSourceDoesNotTakeOverAnotherOne(t *testing.T) {
	const path = "/form/failover/handover"

	outgoing := NewFormSource(path, nil)
	incoming := NewFormSource(path, nil)

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
