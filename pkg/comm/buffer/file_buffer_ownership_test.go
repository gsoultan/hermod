package buffer

import (
	"path/filepath"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
)

// TestProduceDropsItsOwnReferenceNotTheObject covers the ownership contract
// between the spill buffer and the pooled message it is handed.
//
// Produce is documented as taking ownership of the message, and it did that by
// calling message.ReleaseMessage — which resets the message and returns it to
// the pool *unconditionally*, ignoring the reference count. That is not taking
// ownership, it is seizing the object: a caller that still held a reference
// (pendingMessage, or the runner's routed targets) then had its message reset
// and refilled underneath it, and its own later Release drove the count
// negative.
//
// Taking ownership means dropping *your* reference and letting the last owner
// out pool it.
func TestProduceDropsItsOwnReferenceNotTheObject(t *testing.T) {
	before := message.OverReleaseCount()

	buf, err := NewFileBuffer(filepath.Join(t.TempDir(), "spill"), 1<<20)
	if err != nil {
		t.Fatalf("NewFileBuffer: %v", err)
	}
	t.Cleanup(func() { _ = buf.Close() })

	msg := message.AcquireMessage()
	msg.SetID("owned")
	msg.SetPayload([]byte(`{"k":"v"}`))

	// A second owner, exactly as the sink writer has via its pendingMessage.
	msg.Retain()
	if got := msg.RefCount(); got != 2 {
		t.Fatalf("setup: refcount is %d, want 2", got)
	}

	if err := buf.Produce(t.Context(), msg); err != nil {
		t.Fatalf("Produce: %v", err)
	}

	// Produce consumed one reference. The other owner's is still live, and the
	// message must not have been reset out from under it.
	if got := msg.RefCount(); got != 1 {
		t.Errorf("refcount is %d after Produce, want 1: Produce released a reference it "+
			"does not own, or pooled the object while another owner held it", got)
	}
	if got := msg.ID(); got != "owned" {
		t.Errorf("message ID is %q after Produce, want \"owned\": the object was reset "+
			"while still referenced", got)
	}

	// The remaining owner releases normally; that is the one that pools it.
	msg.Release()

	if n := message.OverReleaseCount() - before; n != 0 {
		t.Errorf("%d over-release(s) during a single Produce", n)
	}
}

// TestProduceOnASoleReferenceStillPools is the other half: when Produce is the
// last owner, the message must actually go back to the pool rather than leak.
func TestProduceOnASoleReferenceStillPools(t *testing.T) {
	before := message.OverReleaseCount()

	buf, err := NewFileBuffer(filepath.Join(t.TempDir(), "spill"), 1<<20)
	if err != nil {
		t.Fatalf("NewFileBuffer: %v", err)
	}
	t.Cleanup(func() { _ = buf.Close() })

	msg := message.AcquireMessage()
	msg.SetID("sole")
	msg.SetPayload([]byte(`{"k":"v"}`))

	if err := buf.Produce(t.Context(), msg); err != nil {
		t.Fatalf("Produce: %v", err)
	}

	if got := msg.RefCount(); got != 0 {
		t.Errorf("refcount is %d after Produce with a sole reference, want 0: the message leaked", got)
	}
	if n := message.OverReleaseCount() - before; n != 0 {
		t.Errorf("%d over-release(s)", n)
	}
}
