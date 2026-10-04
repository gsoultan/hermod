package source

import (
	"errors"
	"sync"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

func newMessage(t *testing.T, id string) hermod.Message {
	t.Helper()
	msg := message.AcquireMessage()
	msg.SetID(id)
	t.Cleanup(func() { message.ReleaseMessage(msg) })
	return msg
}

func received(ch chan hermod.Message) string {
	select {
	case msg, ok := <-ch:
		if !ok {
			return "<closed>"
		}
		return msg.ID()
	default:
		return ""
	}
}

// A source built while the path is free receives at once, with no Read first.
// A webhook that wakes a parked workflow is retried as soon as the workflow has
// built its source, before that source has read anything.
func TestTheFirstRegistrationHoldsThePathAtOnce(t *testing.T) {
	r := NewPathRegistry(4)
	ch := r.Register("/p")
	if err := r.Dispatch("/p", newMessage(t, "m1")); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if got := received(ch); got != "m1" {
		t.Errorf("the only registration received %q", got)
	}
}

// The probe: built while another source holds the path, never read, closed.
func TestARegistrationThatIsNeverReadTakesNothing(t *testing.T) {
	r := NewPathRegistry(4)
	running := r.Register("/p")
	probe := r.Register("/p")

	if err := r.Dispatch("/p", newMessage(t, "during")); err != nil {
		t.Fatalf("dispatch while the probe exists: %v", err)
	}
	if got := received(running); got != "during" {
		t.Errorf("while the probe existed the running source received %q", got)
	}

	r.Unregister("/p", probe)

	if err := r.Dispatch("/p", newMessage(t, "after")); err != nil {
		t.Fatalf("dispatch after the probe closed: %v", err)
	}
	if got := received(running); got != "after" {
		t.Errorf("after the probe closed the running source received %q", got)
	}
	if got := received(probe); got != "" {
		t.Errorf("the probe received %q", got)
	}
}

// The handover: the source that starts reading receives, and the outgoing
// one's teardown removes nothing.
func TestTheSourceThatStartsReadingTakesThePathOver(t *testing.T) {
	r := NewPathRegistry(4)
	outgoing := r.Register("/p")
	incoming := r.Register("/p")

	r.TakeOver("/p", incoming)
	if err := r.Dispatch("/p", newMessage(t, "m1")); err != nil {
		t.Fatalf("dispatch after the takeover: %v", err)
	}
	if got := received(incoming); got != "m1" {
		t.Errorf("the source that took over received %q", got)
	}

	r.Unregister("/p", outgoing)
	if err := r.Dispatch("/p", newMessage(t, "m2")); err != nil {
		t.Fatalf("dispatch after the outgoing source closed: %v; its teardown removed "+
			"the source that took over from it", err)
	}
	if got := received(incoming); got != "m2" {
		t.Errorf("after the outgoing source closed, the incoming one received %q", got)
	}
}

// The other order of the same handover: the holder closes before its successor
// has read. The path must pass on, not disappear.
func TestAClosingHolderPassesThePathToTheSourceWaiting(t *testing.T) {
	r := NewPathRegistry(4)
	outgoing := r.Register("/p")
	incoming := r.Register("/p")

	r.Unregister("/p", outgoing)
	if got := received(outgoing); got != "<closed>" {
		t.Errorf("the holder's channel was not closed on release: %q", got)
	}
	if err := r.Dispatch("/p", newMessage(t, "m1")); err != nil {
		t.Fatalf("dispatch after the holder closed: %v", err)
	}
	if got := received(incoming); got != "m1" {
		t.Errorf("the waiting source received %q", got)
	}

	// Its first Read arrives after it already holds the path.
	r.TakeOver("/p", incoming)
	r.Unregister("/p", incoming)
	if err := r.Dispatch("/p", newMessage(t, "m2")); !errors.Is(err, ErrPathNotRegistered) {
		t.Errorf("the path is still registered after its last source closed: %v", err)
	}
}

func TestDispatchSaysWhenTheBufferIsFull(t *testing.T) {
	r := NewPathRegistry(1)
	r.Register("/p")
	if err := r.Dispatch("/p", newMessage(t, "m1")); err != nil {
		t.Fatalf("first dispatch: %v", err)
	}
	if err := r.Dispatch("/p", newMessage(t, "m2")); !errors.Is(err, ErrPathBufferFull) {
		t.Errorf("a dispatch into a full buffer returned %v", err)
	}
}

// Dispatch racing the holder's close must not send on a closed channel. Three
// of the four registries released their lock before sending.
func TestDispatchDoesNotRaceTheHolderClosing(t *testing.T) {
	r := NewPathRegistry(1)
	msg := newMessage(t, "m")
	for range 200 {
		ch := r.Register("/p")
		var wg sync.WaitGroup
		wg.Go(func() { _ = r.Dispatch("/p", msg) })
		wg.Go(func() { r.Unregister("/p", ch) })
		wg.Wait()
	}
}
