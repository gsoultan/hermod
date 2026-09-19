package message

// A pooled message keeps whatever it grew to.
//
// Reset uses clear() on the maps, which empties them but keeps the bucket
// array, and re-slices the buffers to [:0], which keeps the capacity. So one
// 8 MB payload or one 10,000-field row pins that much memory in the pool for
// the life of the process, and a pipeline that sees a single outlier message
// never gives the memory back. sync.Pool's victim cache means GC eventually
// reclaims an *unused* message, but a busy pool keeps its entries alive.
//
// The fix is to keep the common case cheap — reuse the allocation — and hand
// back anything that has grown out of proportion.

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestResetHandsBackAnOversizedPayload(t *testing.T) {
	m := AcquireMessage()
	m.SetPayload([]byte(strings.Repeat("x", 8<<20)))
	m.SetBefore([]byte(strings.Repeat("y", 8<<20)))

	m.Reset()

	if got := cap(m.payload); got > maxPooledBufferBytes {
		t.Errorf("payload keeps %d bytes of capacity after Reset, cap is %d", got, maxPooledBufferBytes)
	}
	if got := cap(m.before); got > maxPooledBufferBytes {
		t.Errorf("before keeps %d bytes of capacity after Reset, cap is %d", got, maxPooledBufferBytes)
	}
}

func TestResetKeepsAnOrdinaryBuffer(t *testing.T) {
	m := AcquireMessage()
	m.SetPayload([]byte(strings.Repeat("x", 4096)))

	m.Reset()

	if cap(m.payload) == 0 {
		t.Error("an ordinary payload buffer was thrown away; pooling exists to reuse it")
	}
}

func TestResetHandsBackAnOversizedDataMap(t *testing.T) {
	m := AcquireMessage()
	for i := range maxPooledMapEntries * 2 {
		m.SetData("col_"+strconv.Itoa(i), i)
		m.SetMetadata("meta_"+strconv.Itoa(i), "v")
	}

	dataBefore := reflect.ValueOf(m.data).Pointer()
	metaBefore := reflect.ValueOf(m.metadata).Pointer()

	m.Reset()

	if reflect.ValueOf(m.data).Pointer() == dataBefore {
		t.Error("the oversized data map was reused, so its buckets stay allocated for the life of the pool")
	}
	if reflect.ValueOf(m.metadata).Pointer() == metaBefore {
		t.Error("the oversized metadata map was reused, so its buckets stay allocated for the life of the pool")
	}
	if len(m.data) != 0 || len(m.metadata) != 0 {
		t.Errorf("Reset left %d data and %d metadata entries", len(m.data), len(m.metadata))
	}
}

func TestResetKeepsAnOrdinaryDataMap(t *testing.T) {
	m := AcquireMessage()
	for i := range 8 {
		m.SetData("col_"+strconv.Itoa(i), i)
	}
	before := reflect.ValueOf(m.data).Pointer()

	m.Reset()

	if reflect.ValueOf(m.data).Pointer() != before {
		t.Error("an ordinary data map was replaced; pooling exists to reuse it")
	}
}

// Whatever Reset does to the storage, a message coming back out of the pool
// must behave like a new one.
func TestMessageIsUsableAfterAnOversizedReset(t *testing.T) {
	m := AcquireMessage()
	m.SetPayload([]byte(strings.Repeat("x", 8<<20)))
	for i := range maxPooledMapEntries * 2 {
		m.SetData("col_"+strconv.Itoa(i), i)
	}
	m.Reset()

	m.SetID("after")
	m.SetPayload([]byte(`{"a":1}`))
	m.SetMetadata("k", "v")

	if got := m.ID(); got != "after" {
		t.Errorf("ID() = %q, want %q", got, "after")
	}
	if got := string(m.Payload()); got != `{"a":1}` {
		t.Errorf("Payload() = %q", got)
	}
	if got := m.PayloadLen(); got != 7 {
		t.Errorf("PayloadLen() = %d, want 7", got)
	}
	if v, ok := m.MetadataValue("k"); !ok || v != "v" {
		t.Errorf("MetadataValue(k) = %q, %v", v, ok)
	}
	if got := m.Data()["a"]; got != float64(1) {
		t.Errorf(`Data()["a"] = %#v, want float64(1)`, got)
	}
}
