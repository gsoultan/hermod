package registry

// Status rows were rewritten on every health tick, saying the same thing.
//
// checkHealth pings every sink once a second and calls setSinkStatus for each
// one, plus SetEngineStatusUnless for the engine. Each of those notifies, and
// the registry's status callback writes a workflow row, a source row and one
// row per sink — synchronously, on the health-check goroutine. With N sinks
// that is (N+1) notifications x (N+2) writes every second, for the life of the
// workflow, and the value written is almost always the one already there.
//
// The callback's own comment says "as they change rarely", which is the
// assumption it was written on and was not true. The notifications themselves
// have to keep flowing — they are the only thing that pushes per-workflow
// status to the UI, and there is no periodic floor behind them — so the fix is
// to stop writing what is already stored.

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gsoultan/hermod/pkg/engine/telemetry"
)

// countingStatusStore records every status write, and can be made to fail.
type countingStatusStore struct {
	pipeStorage
	mu        sync.Mutex
	workflows []string
	sources   []string
	sinks     []string
	failNext  atomic.Bool
}

func (s *countingStatusStore) UpdateWorkflowStatus(ctx context.Context, id, status string) error {
	if s.failNext.Load() {
		return errors.New("storage down")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workflows = append(s.workflows, id+"="+status)
	return nil
}

func (s *countingStatusStore) UpdateSourceStatus(ctx context.Context, id, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sources = append(s.sources, id+"="+status)
	return nil
}

func (s *countingStatusStore) UpdateSinkStatus(ctx context.Context, id, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sinks = append(s.sinks, id+"="+status)
	return nil
}

func (s *countingStatusStore) counts() (int, int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.workflows), len(s.sources), len(s.sinks)
}

func healthyUpdate() telemetry.StatusUpdate {
	return telemetry.StatusUpdate{
		WorkflowID:   "wf",
		EngineStatus: "running",
		SourceID:     "src-1",
		SourceStatus: "running",
		SinkStatuses: map[string]string{"snk-1": "running", "snk-2": "running"},
	}
}

func gatedRegistry(t *testing.T) (*Registry, *countingStatusStore) {
	t.Helper()
	store := &countingStatusStore{}
	return NewRegistry(store), store
}

func TestStatusWritesSkipUnchangedValues(t *testing.T) {
	r, store := gatedRegistry(t)
	gate := newStatusWriteGate()
	var dlqAlerted atomic.Bool

	// First pass establishes what is stored: everything is written.
	r.applyStatusUpdate(context.Background(), "wf", gate, healthyUpdate(), &dlqAlerted)
	wf, src, snk := store.counts()
	if wf != 1 || src != 1 || snk != 2 {
		t.Fatalf("first pass wrote workflow=%d source=%d sinks=%d, want 1/1/2", wf, src, snk)
	}

	// Ten more health ticks with nothing changed.
	for range 10 {
		r.applyStatusUpdate(context.Background(), "wf", gate, healthyUpdate(), &dlqAlerted)
	}
	wf, src, snk = store.counts()
	if wf != 1 || src != 1 || snk != 2 {
		t.Errorf("ten unchanged ticks wrote workflow=%d source=%d sinks=%d, want 1/1/2 still: "+
			"a healthy workflow rewrites its status rows for ever", wf, src, snk)
	}
}

func TestStatusWritesStillLandWhenSomethingChanges(t *testing.T) {
	r, store := gatedRegistry(t)
	gate := newStatusWriteGate()
	var dlqAlerted atomic.Bool

	r.applyStatusUpdate(context.Background(), "wf", gate, healthyUpdate(), &dlqAlerted)

	// One sink starts reconnecting: that sink is written, nothing else is.
	u := healthyUpdate()
	u.SinkStatuses["snk-2"] = "reconnecting"
	r.applyStatusUpdate(context.Background(), "wf", gate, u, &dlqAlerted)

	wf, src, snk := store.counts()
	if wf != 1 || src != 1 {
		t.Errorf("a sink change rewrote workflow=%d source=%d rows; want 1/1", wf, src)
	}
	if snk != 3 {
		t.Errorf("sink writes = %d, want 3 (two initial plus the one that changed)", snk)
	}

	// The engine going down must be written.
	u.EngineStatus = "Stopped"
	r.applyStatusUpdate(context.Background(), "wf", gate, u, &dlqAlerted)
	if wf, _, _ = store.counts(); wf != 2 {
		t.Errorf("workflow writes = %d after the engine stopped, want 2", wf)
	}
}

// A write that fails must be retried on the next tick. Recording a value as
// stored when storage refused it would drop the status permanently — the UI
// would show the old one for ever, and this is the path that exists to make
// failures visible.
func TestAFailedStatusWriteIsRetried(t *testing.T) {
	r, store := gatedRegistry(t)
	gate := newStatusWriteGate()
	var dlqAlerted atomic.Bool

	store.failNext.Store(true)
	r.applyStatusUpdate(context.Background(), "wf", gate, healthyUpdate(), &dlqAlerted)
	if wf, _, _ := store.counts(); wf != 0 {
		t.Fatalf("precondition: storage accepted %d writes while failing", wf)
	}

	store.failNext.Store(false)
	r.applyStatusUpdate(context.Background(), "wf", gate, healthyUpdate(), &dlqAlerted)
	if wf, _, _ := store.counts(); wf != 1 {
		t.Errorf("workflow writes = %d after storage recovered, want 1: a failed write was recorded as stored", wf)
	}
}

// The gate is reached from the health-check goroutine and from whatever else
// changes a status, so it is exercised concurrently.
func TestStatusWriteGateUnderConcurrency(t *testing.T) {
	r, store := gatedRegistry(t)
	gate := newStatusWriteGate()
	var dlqAlerted atomic.Bool

	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 50 {
				u := healthyUpdate()
				// Half the goroutines hold the value steady; the rest churn a
				// sink so there is something to write.
				if g%2 == 1 {
					u.SinkStatuses["snk-1"] = "s" + strconv.Itoa(i)
				}
				r.applyStatusUpdate(context.Background(), "wf", gate, u, &dlqAlerted)
			}
		})
	}
	wg.Wait()

	wf, src, _ := store.counts()
	if wf != 1 {
		t.Errorf("workflow status written %d times under concurrency; it never changed", wf)
	}
	if src != 1 {
		t.Errorf("source status written %d times under concurrency; it never changed", src)
	}
}
