package registry

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

// slowLogCreator stands in for log storage that is reachable but slow — a
// loaded platform API, or one behind a saturated link.
type slowLogCreator struct {
	delay time.Duration

	mu    sync.Mutex
	calls int
}

func (s *slowLogCreator) CreateLog(context.Context, storage.Log) error { return nil }

func (s *slowLogCreator) CreateLogs(context.Context, []storage.Log) error {
	time.Sleep(s.delay)
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	return nil
}

// The engine logs from its own goroutines — the source ingestion loop, the sink
// writers, the message processors. A flush that runs inline on whichever
// goroutine happened to fill the buffer puts a network round trip to the
// platform directly in the pipeline's path, which turns a slow log endpoint
// into a slow pipeline. Worse, that is most likely precisely when things are
// already going wrong and the engine is logging hardest.
func TestDatabaseLoggerDoesNotBlockTheCallerOnAFullBuffer(t *testing.T) {
	slow := &slowLogCreator{delay: 2 * time.Second}
	l := NewDatabaseLogger(context.Background(), slow, "wf-1", &captureLogger{})
	defer l.Close()

	// Fill well past the flush trigger.
	start := time.Now()
	for i := range dbLogBufferFlushAt * 3 {
		l.Info("line", "n", i)
	}
	elapsed := time.Since(start)

	if elapsed > time.Second {
		t.Errorf("logging %d lines took %s: a slow log endpoint is stalling the engine goroutine that emitted them",
			dbLogBufferFlushAt*3, elapsed)
	}
}

// A worker whose platform is unreachable keeps running and keeps logging. If the
// buffer grows for every line that cannot be shipped, the process eventually
// dies of it — trading a visibility problem for an outage.
func TestDatabaseLoggerBoundsItsBuffer(t *testing.T) {
	failing := failingLogCreator{err: errors.New("platform unreachable")}
	fallback := &captureLogger{}
	l := NewDatabaseLogger(context.Background(), failing, "wf-1", fallback)
	defer l.Close()

	for i := range dbLogBufferMax * 4 {
		l.Error("line", "n", i)
	}

	if n := l.buffered(); n > dbLogBufferMax {
		t.Errorf("buffer holds %d entries, above the %d cap: an unreachable platform would grow the worker's memory without limit",
			n, dbLogBufferMax)
	}

	// Dropping is acceptable; dropping silently is not.
	l.Flush()
	if !fallback.contains("dropped") {
		t.Errorf("entries were discarded with nothing said about it; log=%v", fallback.lines)
	}
}

// Whatever happens to the database copy, the process log is the floor: it is the
// one destination that does not depend on the component most likely to be down.
func TestDatabaseLoggerKeepsTeeingWhileStorageIsDown(t *testing.T) {
	failing := failingLogCreator{err: errors.New("platform unreachable")}
	fallback := &captureLogger{}
	l := NewDatabaseLogger(context.Background(), failing, "wf-1", fallback)
	defer l.Close()

	for i := range dbLogBufferMax * 2 {
		l.Error("Pipeline stalled", "n", i)
	}

	if got := fallback.count("Pipeline stalled"); got != dbLogBufferMax*2 {
		t.Errorf("process log received %d of %d stall reports while storage was down", got, dbLogBufferMax*2)
	}
}
