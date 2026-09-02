package engine

import (
	"sync"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
)

// releasePendingMessage guarded the pool Put with `refCount.Add(-1) > 0`, which
// returns early only for a POSITIVE remainder. A third release drove the count
// to -1 — not > 0 — so it fell through and returned the same object to the pool
// a second time. The pool then handed one *pendingMessage to two goroutines,
// each of which wrote pm.msg in acquire and read it in enqueueWithStrategy:
//
//	WARNING: DATA RACE
//	  Write at acquirePendingMessage()  writer.go:82
//	  Read  at enqueueWithStrategy()    writer.go:850
//
// Two owners also share one done channel of capacity 1, so one consumes the
// other's completion signal and a message is never confirmed — which is the
// intermittent "sent 10 messages, but sink received only 9" in
// TestEngineGracefulShutdown.
//
// There are 14 release sites against 2 acquire sites, so over-release must be
// survivable rather than merely forbidden.
func TestReleasePendingMessageNeverDoublePools(t *testing.T) {
	allowPendingOverReleases(1) // the third release below
	msg := message.AcquireMessage()
	msg.SetPayload([]byte(`{"k":"v"}`))

	pm := acquirePendingMessage(msg) // refCount = 2

	releasePendingMessage(pm) // 2 -> 1, no pool
	releasePendingMessage(pm) // 1 -> 0, pooled exactly once
	releasePendingMessage(pm) // 0 -> -1, must NOT pool a second time

	a := pendingMessagePool.Get().(*pendingMessage)
	b := pendingMessagePool.Get().(*pendingMessage)
	if a == b {
		t.Fatal("pool returned the same *pendingMessage twice: one object is now owned by two goroutines")
	}
}

// The reference count must not be left negative, or the next acquire/release
// cycle on a recycled object pools it early — the same failure one step later.
func TestReleasePendingMessageDoesNotLeaveNegativeRefCount(t *testing.T) {
	allowPendingOverReleases(3) // 5 releases against 2 references
	msg := message.AcquireMessage()
	msg.SetPayload([]byte(`{"k":"v"}`))

	pm := acquirePendingMessage(msg)
	for range 5 {
		releasePendingMessage(pm)
	}

	if got := pm.refCount.Load(); got < 0 {
		t.Fatalf("refCount left at %d; a recycled object would pool early", got)
	}
}

// The real shape: many goroutines releasing the same pendingMessage, as the
// producer, the sink writer and the drop_oldest evictor all do. Run under -race.
func TestReleasePendingMessageConcurrentOverRelease(t *testing.T) {
	allowPendingOverReleases(200 * 2) // 4 releasers against 2 references, 200 rounds
	for range 200 {
		msg := message.AcquireMessage()
		msg.SetPayload([]byte(`{"k":"v"}`))
		pm := acquirePendingMessage(msg)

		var wg sync.WaitGroup
		for range 4 { // one more releaser than the two references held
			wg.Go(func() { releasePendingMessage(pm) })
		}
		wg.Wait()

		if got := pm.refCount.Load(); got < 0 {
			t.Fatalf("refCount %d after concurrent over-release", got)
		}
	}
}
