package hermod_test

import (
	"strconv"
	"sync"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// TestOrderingKeyDoesNotRaceWithSetMetadata reproduces the second crash of the
// shape that took the engine down through ToMap:
//
//	fatal error: concurrent map read and map write
//
// OrderingKey reads the metadata map by reference and indexes it with no lock
// held. Its doc comment justified that on ownership -- "the caller owns the
// message at that point, it has been taken off the buffer and not yet handed to
// a worker" -- which is true of the dispatch path it was written for and false
// of the caller it since acquired.
//
// sinkWriter.pickShard calls it, and pickShard runs inside the per-sink enqueue
// goroutines that runner.go fans out with swg.Go: one goroutine per target, all
// holding the same message. By the time the second sink is being enqueued, the
// first sink's worker can already be inside writeToSink, where recordFailure
// writes _hermod_failed_sink and recordTraceStepAt writes _hermod_lineage --
// both through the locked SetMetadata. An unlocked read against a locked write
// is still a race, and on a map the runtime makes it fatal.
//
// Sharding is enough to reach it; no shardKeyMeta needs to be configured,
// because OrderingKey is the fallback pickShard always takes.
func TestOrderingKeyDoesNotRaceWithSetMetadata(t *testing.T) {
	m := message.AcquireMessage()
	defer m.Release()
	m.SetMetadata(hermod.MetaOrderingKey, "public.orders:1")

	const iterations = 5000
	var wg sync.WaitGroup
	wg.Add(2)

	// A per-sink enqueue goroutine choosing a shard.
	go func() {
		defer wg.Done()
		for range iterations {
			if got := hermod.OrderingKey(m); got != "public.orders:1" {
				t.Errorf("ordering key = %q, want public.orders:1", got)
				return
			}
		}
	}()

	// The other sink's worker, recording a failure against the same message.
	go func() {
		defer wg.Done()
		for i := range iterations {
			m.SetMetadata("_hermod_failed_at", strconv.Itoa(i))
		}
	}()

	wg.Wait()
}

// TestMetadataValueDoesNotRaceWithSetMetadata covers the same collision through
// the accessor the fix routes every caller to, so the replacement is held to the
// property the old path lacked rather than merely assumed to have it.
func TestMetadataValueDoesNotRaceWithSetMetadata(t *testing.T) {
	m := message.AcquireMessage()
	defer m.Release()
	m.SetMetadata("_hermod_workflow_id", "wf-1")

	const iterations = 5000
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for range iterations {
			if got, ok := hermod.MetadataValue(m, "_hermod_workflow_id"); !ok || got != "wf-1" {
				t.Errorf("workflow id = %q (ok=%v), want wf-1", got, ok)
				return
			}
		}
	}()

	go func() {
		defer wg.Done()
		for i := range iterations {
			m.SetMetadata("_hermod_lineage", "node-"+strconv.Itoa(i))
		}
	}()

	wg.Wait()
}
