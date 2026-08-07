package message

import (
	"testing"
	"time"
)

// Clone used to take a write lock on the freshly-pooled clone while still
// holding a read lock on the source. When a caller cloned a message whose
// refcount had already reached zero, the pool handed back that same object and
// the two locks deadlocked against each other — an unkillable hang that took
// out the whole test binary after 10 minutes rather than surfacing the
// refcount bug.
//
// This reproduces exactly that shape: release a message back to the pool, then
// clone through the stale reference. The clone must complete rather than hang.
func TestCloneAfterPoolReuseDoesNotDeadlock(t *testing.T) {
	m := AcquireMessage()
	m.SetID("stale")
	m.SetData("k", "v")

	// Drop the last reference: the message goes back into the pool while this
	// test still holds a pointer to it, which is precisely the misuse that
	// triggered the hang.
	m.Release()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = m.Clone()
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Clone deadlocked on a pool-reused message")
	}
}

// A clone must be an independent copy: mutating it must not write through to
// the original, which shares backing maps until copied.
func TestCloneIsIndependent(t *testing.T) {
	orig := AcquireMessage()
	defer orig.Release()
	orig.SetID("orig")
	orig.SetData("shared", "original")
	orig.SetMetadata("meta", "original")

	clone := orig.Clone()
	clone.SetData("shared", "mutated")
	clone.SetMetadata("meta", "mutated")

	if got := orig.Data()["shared"]; got != "original" {
		t.Errorf("clone mutated original data: got %v; want \"original\"", got)
	}
	if got := orig.Metadata()["meta"]; got != "original" {
		t.Errorf("clone mutated original metadata: got %v; want \"original\"", got)
	}
	if got := clone.Data()["shared"]; got != "mutated" {
		t.Errorf("clone did not take its own value: got %v; want \"mutated\"", got)
	}
}
