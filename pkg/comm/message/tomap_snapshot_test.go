package message

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/gsoultan/hermod"
)

// newNestedMsg builds a message holding a nested object, the way a source with a
// jsonb column does, and then reads it once.
//
// The read matters. ToMap merges m.data into the snapshot, and until something
// hydrates the payload m.data is empty — so a snapshot taken before the first
// read decodes the payload into a fresh map and happens to share nothing. By the
// time the engine records a trace step the message has been read: the validator
// and every transformation ahead of it go through Data()/DataRef(). That is the
// order these tests use, because it is the order production runs in.
func newNestedMsg(tb testing.TB) *DefaultMessage {
	tb.Helper()
	m := AcquireMessage()
	m.SetPayload([]byte(`{"id":"1","customer":{"name":"ACME","tier":"gold"}}`))
	_ = m.DataRef()
	return m
}

// TestToMapDoesNotAliasNestedMaps pins the invariant a trace step depends on: a
// snapshot must not change when the message it was taken from is written to.
//
// ToMap used to copy only the top level (maps.Copy), so every nested
// map[string]any in the snapshot was the same map the live message holds. A
// transformation with a dotted targetField -- "customer.name" -- walks into that
// nested map and assigns in place (SetData, message.go), so the write landed
// inside a snapshot that had already been handed to someone else.
func TestToMapDoesNotAliasNestedMaps(t *testing.T) {
	m := newNestedMsg(t)
	defer m.Release()

	snap := m.ToMap()

	m.SetData("customer.name", "CHANGED")

	cust, ok := snap["customer"].(map[string]any)
	if !ok {
		t.Fatalf("snapshot lost the nested object: customer = %#v", snap["customer"])
	}
	if got := cust["name"]; got != "ACME" {
		t.Errorf("snapshot mutated after ToMap returned: customer.name = %v, want ACME", got)
	}
}

// TestToMapSnapshotSurvivesConcurrentDottedWrite reproduces the production
// crash.
//
//	fatal error: concurrent map iteration and map write
//	encoding/json/v2.marshalObjectAny
//	internal/storage/sql.(*sqlStorage).RecordTraceStep.func1
//	pkg/engine.(*Engine).recordTraceStepAt.func1
//
// recordTraceStepAt hands step.After to a detached goroutine that marshals it
// against PostgreSQL under a five-second timeout, holding no lock, while the
// pipeline keeps transforming the same message. With the snapshot's nested maps
// aliased to the live message, a dotted SetData writes the map json.Marshal is
// iterating.
//
// Concurrent map access is a runtime fatal error, not a panic, so the recover()
// that guards that marshal (sql.go safeMarshal) cannot contain it -- the process
// exits 2 and systemd restarts the engine. Before the fix this test takes the
// test binary down the same way; there is no assertion that can catch it, which
// is the point.
func TestToMapSnapshotSurvivesConcurrentDottedWrite(t *testing.T) {
	m := newNestedMsg(t)
	defer m.Release()

	snap := m.ToMap()

	const iterations = 2000
	var wg sync.WaitGroup
	wg.Add(2)

	// The detached trace recorder.
	go func() {
		defer wg.Done()
		for range iterations {
			if _, err := json.Marshal(snap); err != nil {
				t.Errorf("marshalling the trace snapshot failed: %v", err)
				return
			}
		}
	}()

	// The pipeline, still running transformations on the same message.
	go func() {
		defer wg.Done()
		for i := range iterations {
			m.SetData("customer.name", fmt.Sprintf("ACME-%d", i))
		}
	}()

	wg.Wait()
}

// TestToMapCDCEnvelopeBytesAreCopied covers the same defect on the other half of
// a CDC snapshot.
//
// ToMap exposes the before/after images as json.RawMessage over the message's
// own byte slices, and both SetBefore and SetPayload write back into those
// slices with append(b[:0], ...) rather than allocating -- so the next write
// rewrites the bytes a snapshot is still pointing at. Sequentially that shows a
// trace step or a live-viewer frame content captured after its own timestamp;
// concurrently it is a data race on the backing array.
//
// deepCopyValue's []byte case does not cover this on its own: json.RawMessage is
// a named type and a type switch on []byte does not match it.
func TestToMapCDCEnvelopeBytesAreCopied(t *testing.T) {
	m := AcquireMessage()
	defer m.Release()
	m.SetOperation(hermod.OpCreate)
	m.SetTable("orders")
	m.SetBefore([]byte(`{"id":"1","amount":"10.50"}`))
	m.SetAfter([]byte(`{"id":"1","amount":"11.50"}`))

	snap := m.ToMap()
	before, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshalling the snapshot failed: %v", err)
	}

	// Same lengths as the originals, so an in-place append overwrites every byte.
	m.SetBefore([]byte(`{"id":"9","amount":"99.99"}`))
	m.SetAfter([]byte(`{"id":"9","amount":"98.99"}`))

	after, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("re-marshalling the snapshot failed: %v", err)
	}

	if !bytes.Equal(before, after) {
		t.Errorf("snapshot changed after the message was written to:\n before: %s\n  after: %s", before, after)
	}
}
