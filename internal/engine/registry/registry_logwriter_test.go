package registry

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/testutil"
)

// slowLogStore is log storage that stops answering: every CreateLog blocks
// until its context ends or the test releases it. It is the condition 3.3 of
// the performance review describes — a failing node logging every message
// while the log table is slow — reduced to its worst case.
type slowLogStore struct {
	testutil.BaseMockStorage
	release  chan struct{}
	inFlight atomic.Int64
	written  atomic.Int64
}

func newSlowLogStore() *slowLogStore {
	return &slowLogStore{release: make(chan struct{})}
}

func (s *slowLogStore) CreateLog(ctx context.Context, _ storage.Log) error {
	s.inFlight.Add(1)
	defer s.inFlight.Add(-1)
	select {
	case <-s.release:
		s.written.Add(1)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// recordingLogStore answers at once and keeps what it was given.
type recordingLogStore struct {
	testutil.BaseMockStorage
	mu   sync.Mutex
	logs []storage.Log
}

func (s *recordingLogStore) CreateLog(_ context.Context, l storage.Log) error {
	s.mu.Lock()
	s.logs = append(s.logs, l)
	s.mu.Unlock()
	return nil
}

func (s *recordingLogStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.logs)
}

// settledGoroutines waits for the goroutine count to stop exceeding limit and
// returns the last count it saw.
func settledGoroutines(limit int, within time.Duration) int {
	deadline := time.Now().Add(within)
	n := runtime.NumGoroutine()
	for n > limit && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		runtime.GC()
		n = runtime.NumGoroutine()
	}
	return n
}

// TestBroadcastLogBoundsGoroutinesWhenStorageBlocks is the out-of-memory path:
// BroadcastLog used to start a goroutine per line, each holding an untimed
// insert, so a node failing every message against a stalled log table grew the
// goroutine count with the message rate until the process died.
func TestBroadcastLogBoundsGoroutinesWhenStorageBlocks(t *testing.T) {
	store := newSlowLogStore()
	reg := NewRegistry(store)
	t.Cleanup(func() {
		close(store.release)
		reg.Close()
	})

	before := runtime.NumGoroutine()

	const lines = 5000
	start := time.Now()
	for i := range lines {
		reg.BroadcastLog("wf-failing", "ERROR", fmt.Sprintf("node failed %d", i), "")
	}
	// The pipeline must never wait on log storage.
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("BroadcastLog took %s for %d lines against blocked storage; it must not block the pipeline", took, lines)
	}

	// A small fixed pool may be writing; nothing proportional to the volume.
	const headroom = 32
	if grew := runtime.NumGoroutine() - before; grew > headroom {
		t.Fatalf("goroutines grew by %d after %d log lines against blocked storage, want at most %d: "+
			"log writes are not bounded", grew, lines, headroom)
	}
	if in := store.inFlight.Load(); in > headroom {
		t.Fatalf("%d concurrent CreateLog calls against blocked storage, want at most %d", in, headroom)
	}
	if reg.LogWritesDropped() == 0 {
		t.Errorf("no log writes reported dropped after %d lines against blocked storage; "+
			"a full queue must drop and count, not grow", lines)
	}
}

// TestBroadcastLogTimesOutStalledWrites: a write that storage never answers
// must give its worker back, or a few stalled inserts stop log persistence
// for the life of the process.
func TestBroadcastLogTimesOutStalledWrites(t *testing.T) {
	store := newSlowLogStore()
	reg := NewRegistry(store)
	t.Cleanup(func() {
		close(store.release)
		reg.Close()
	})
	reg.logWriteTimeout = 50 * time.Millisecond

	reg.BroadcastLog("wf-1", "ERROR", "first", "")
	if !waitUntil(t, 5*time.Second, "a write to start", func() bool { return store.inFlight.Load() > 0 }) {
		t.Fatal("the log write never reached storage")
	}
	if !waitUntil(t, 5*time.Second, "the stalled write to time out", func() bool { return store.inFlight.Load() == 0 }) {
		t.Fatalf("a stalled CreateLog was still in flight after its timeout; writes are untimed")
	}
}

// TestBroadcastLogStillReachesStorageAndSubscribers: bounding the writer must
// not cost the lines themselves when storage keeps up.
func TestBroadcastLogStillReachesStorageAndSubscribers(t *testing.T) {
	store := &recordingLogStore{}
	reg := NewRegistry(store)
	t.Cleanup(reg.Close)

	reg.mu.Lock()
	reg.engines["wf-live"] = &activeEngine{isWorkflow: true}
	reg.mu.Unlock()

	global := reg.SubscribeLogs()
	perWF := reg.SubscribeWorkflowLogs("wf-live")
	t.Cleanup(func() {
		reg.UnsubscribeLogs(global)
		reg.UnsubscribeLogs(perWF)
	})

	const lines = 50
	for i := range lines {
		reg.BroadcastLog("wf-live", "INFO", fmt.Sprintf("line %d", i), "")
	}

	if !waitUntil(t, 5*time.Second, "every line persisted", func() bool { return store.count() == lines }) {
		t.Fatalf("storage holds %d of %d lines", store.count(), lines)
	}

	for name, ch := range map[string]chan storage.Log{"global": global, "workflow": perWF} {
		got := 0
		timeout := time.After(5 * time.Second)
	drain:
		for got < lines {
			select {
			case l := <-ch:
				if l.WorkflowID != "wf-live" {
					t.Fatalf("%s subscriber got a line for workflow %q, want wf-live", name, l.WorkflowID)
				}
				got++
			case <-timeout:
				break drain
			}
		}
		if got != lines {
			t.Errorf("%s subscriber received %d of %d lines", name, got, lines)
		}
	}
	if d := reg.LogWritesDropped(); d != 0 {
		t.Errorf("%d log writes dropped with fast storage, want 0", d)
	}
}

// TestBroadcastLogWritersDoNotLeakAfterClose is the shutdown contract: the
// writer pool belongs to the registry and goes when it does, including writers
// blocked on a storage call that will never return on its own.
func TestBroadcastLogWritersDoNotLeakAfterClose(t *testing.T) {
	before := runtime.NumGoroutine()

	const cycles = 10
	for range cycles {
		store := newSlowLogStore()
		reg := NewRegistry(store)
		for i := range 100 {
			reg.BroadcastLog("wf-x", "ERROR", fmt.Sprintf("line %d", i), "")
		}
		reg.Close()
	}

	if after := settledGoroutines(before+4, 10*time.Second); after > before+4 {
		t.Errorf("goroutines grew from %d to %d after %d registry open/close cycles; "+
			"log writers outlive the registry", before, after, cycles)
	}
}
