package registry

import (
	"context"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// stopEngine has to leave the registry consistent even when its caller is gone.
//
// The first thing it does is cancel the engine, so from that point the engine is
// coming down whatever else happens. The bookkeeping that says "this workflow is
// running on this worker" was only updated at the very end, after a select on
// the caller's context — so a caller whose context had already been cancelled
// returned early and left the entry behind for good.
//
// That is exactly the shape of a failover. The worker losing its lease has had
// its context cancelled; its stop returns immediately, the entry stays, and
// IsEngineRunning keeps answering true for an engine that is gone. The worker
// taking over then tries to stop it before starting its own, hits the same
// early return, and retries every sync interval forever. The workflow is
// reported running, receives nothing, and cannot be restarted without bouncing
// the process.
//
// When both the engine's done channel and the caller's context are ready, Go
// picks a case at random — which is why this appeared as an occasional
// end-to-end flake rather than an obvious failure.
// ---------------------------------------------------------------------------

// quietLogger keeps stopEngine's warnings out of the test output. A nil logger
// is not the case under test.
type quietLogger struct{}

func (quietLogger) Debug(string, ...any) {}
func (quietLogger) Info(string, ...any)  {}
func (quietLogger) Warn(string, ...any)  {}
func (quietLogger) Error(string, ...any) {}

// newTestRegistry builds a registry with just enough wired up to stop an engine.
func newTestRegistry() *Registry {
	return &Registry{engines: map[string]*activeEngine{}, logger: quietLogger{}}
}

// registeredEngine puts a stoppable entry in the registry, standing in for a
// running workflow without needing a real engine.
func registeredEngine(t *testing.T, r *Registry, id string) chan struct{} {
	t.Helper()
	done := make(chan struct{})
	_, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	r.engines[id] = &activeEngine{cancel: cancel, done: done}
	r.mu.Unlock()
	return done
}

func TestStopEngineClearsTheEntryWhenTheCallerIsCancelled(t *testing.T) {
	r := newTestRegistry()
	const id = "wf-1"

	done := registeredEngine(t, r, id)
	// The engine shuts down, as it must once stopEngine cancels it — just not
	// before the cancelled caller gives up waiting.
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(done)
	}()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the caller has already lost its lease

	_ = r.stopEngine(ctx, id, false)

	if r.IsEngineRunning(id) {
		t.Error("the workflow is still registered as running after being stopped; " +
			"a takeover can never start it and every retry hits the same early return")
	}
}

func TestStopEngineClearsTheEntryOnASuccessfulStop(t *testing.T) {
	r := newTestRegistry()
	const id = "wf-2"

	done := registeredEngine(t, r, id)
	close(done)

	if err := r.stopEngine(context.Background(), id, false); err != nil {
		t.Fatalf("stopEngine: %v", err)
	}
	if r.IsEngineRunning(id) {
		t.Error("the entry survived a clean stop")
	}
}

// TestStopEngineLeavesASupersedingEngineAlone. A stop must only retire the
// engine it was called for. If a takeover has already registered a replacement
// under the same id, removing it by key would stop the workflow that had just
// been correctly started — the same ownership mistake the webhook registry made.
func TestStopEngineLeavesASupersedingEngineAlone(t *testing.T) {
	r := newTestRegistry()
	const id = "wf-3"

	outgoing := registeredEngine(t, r, id)
	close(outgoing)

	r.mu.Lock()
	old := r.engines[id]
	r.mu.Unlock()

	// The replacement registers before the outgoing stop finishes its work.
	replacement := make(chan struct{})
	_, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	r.engines[id] = &activeEngine{cancel: cancel, done: replacement}
	r.mu.Unlock()

	// Now the outgoing stop completes, operating on the entry it was given.
	r.mu.Lock()
	r.engines[id+"-tmp"] = old
	r.mu.Unlock()
	_ = r.stopEngine(context.Background(), id+"-tmp", false)

	if !r.IsEngineRunning(id) {
		t.Error("a finishing stop retired the engine that had just taken over")
	}
}
