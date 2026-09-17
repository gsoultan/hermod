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

// Independence has to hold for nested values too, and that is the case that
// actually occurs: a jsonb column decodes to a map[string]any, and the traversal
// clones a message once per branch whenever a node fans out. maps.Copy copies
// the top level only, so both branches hold the *same* nested map — a
// transformation writing into it on one branch is visible on the other, and the
// two branches deliver each other's data.
func TestCloneIsIndependentForNestedValues(t *testing.T) {
	orig := AcquireMessage()
	defer orig.Release()
	orig.SetID("orig")
	orig.SetData("doc", map[string]any{"status": "original"})
	orig.SetData("tags", []any{"a"})

	clone := orig.Clone()
	defer clone.Release()

	// What a transformation on the cloned branch does.
	if doc, ok := clone.Data()["doc"].(map[string]any); ok {
		doc["status"] = "mutated"
	}
	if tags, ok := clone.Data()["tags"].([]any); ok && len(tags) > 0 {
		tags[0] = "b"
	}

	origDoc, _ := orig.Data()["doc"].(map[string]any)
	if origDoc == nil || origDoc["status"] != "original" {
		t.Errorf("a branch mutating a nested map wrote through to the other branch: "+
			"got %v, want status \"original\"", origDoc)
	}
	origTags, _ := orig.Data()["tags"].([]any)
	if len(origTags) == 0 || origTags[0] != "a" {
		t.Errorf("a branch mutating a nested slice wrote through to the other branch: "+
			"got %v, want [a]", origTags)
	}
}
