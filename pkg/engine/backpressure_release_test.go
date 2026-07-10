package engine

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/engine/config"
)

// countingMessage is a minimal hermod.Message implementation that records how
// many times Release() has been invoked, so tests can assert that a pending
// message is released exactly once.
type countingMessage struct {
	id           string
	releaseCount atomic.Int32
}

func (m *countingMessage) ID() string                     { return m.id }
func (m *countingMessage) Operation() hermod.Operation    { return hermod.OpCreate }
func (m *countingMessage) Table() string                  { return "test" }
func (m *countingMessage) Schema() string                 { return "public" }
func (m *countingMessage) Before() []byte                 { return nil }
func (m *countingMessage) After() []byte                  { return nil }
func (m *countingMessage) Payload() []byte                { return nil }
func (m *countingMessage) Metadata() map[string]string    { return nil }
func (m *countingMessage) MetadataRef() map[string]string { return nil }
func (m *countingMessage) Data() map[string]any           { return nil }
func (m *countingMessage) DataRef() map[string]any        { return nil }
func (m *countingMessage) SetData(key string, value any)  {}
func (m *countingMessage) SetMetadata(key, value string)  {}
func (m *countingMessage) Clone() hermod.Message          { return m }
func (m *countingMessage) ToMap() map[string]any          { return nil }
func (m *countingMessage) ClearPayloads()                 {}
func (m *countingMessage) Retain()                        {}
func (m *countingMessage) Release()                       { m.releaseCount.Add(1) }

// TestBackpressureDropOldest_ReleasesEvictedMessageOnce verifies that when the
// drop_oldest strategy evicts an in-flight pending message, the eviction only
// signals the owning goroutine (which is the single owner responsible for
// releasing it) and does not release the message itself. Previously the
// eviction path and the owner both released the same pending message, causing a
// double release / pool corruption.
func TestBackpressureDropOldest_ReleasesEvictedMessageOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	e := NewEngine(nil, nil, nil)
	e.logger = NewDefaultLogger()

	sw := &sinkWriter{
		engine: e,
		sinkID: "sink-drop-oldest",
		config: config.SinkConfig{BackpressureStrategy: config.BPDropOldest},
		ch:     make(chan *pendingMessage, 1),
	}

	oldMsg := &countingMessage{id: "old"}
	pmOld := acquirePendingMessage(oldMsg)
	sw.ch <- pmOld // fill the buffer

	newMsg := &countingMessage{id: "new"}
	pmNew := acquirePendingMessage(newMsg)
	sw.enqueueWithStrategy(ctx, pmNew, config.BPDropOldest)

	// The evicted (old) pending message must receive the drop error on its done
	// channel; the eviction must NOT have released it.
	select {
	case err := <-pmOld.done:
		if err == nil {
			t.Fatal("expected drop error on evicted message, got nil")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for drop signal on evicted message")
	}

	if got := oldMsg.releaseCount.Load(); got != 0 {
		t.Fatalf("evicted message released by eviction path; want 0 release, got %d", got)
	}

	// The single owner now releases the evicted pending message exactly once.
	releasePendingMessage(pmOld)
	if got := oldMsg.releaseCount.Load(); got != 1 {
		t.Fatalf("owner release: want exactly 1 release, got %d", got)
	}

	// A stray double-release must be a no-op (idempotent guard) and must not
	// corrupt the pool or drive the refcount negative.
	releasePendingMessage(pmOld)
	if got := oldMsg.releaseCount.Load(); got != 1 {
		t.Fatalf("double release must be a no-op; want 1 release, got %d", got)
	}

	// The newest message should be enqueued in the channel.
	select {
	case got := <-sw.ch:
		if got == nil || got.msg == nil || got.msg.ID() != "new" {
			t.Fatalf("expected newest message enqueued after drop_oldest, got %+v", got)
		}
		releasePendingMessage(got)
	default:
		t.Fatal("expected newest message present in channel after drop_oldest")
	}
}
