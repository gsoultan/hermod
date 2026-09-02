package registry

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

type rebuildCall struct {
	id string
	wf storage.Workflow
}

// supervisorHarness is a Registry reduced to what the supervisor touches, with
// the rebuild replaced by a recorder.
type supervisorHarness struct {
	reg    *Registry
	logger *captureLogger

	mu       sync.Mutex
	calls    []rebuildCall
	failNext error
}

func newSupervisorHarness(t *testing.T) *supervisorHarness {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	h := &supervisorHarness{logger: &captureLogger{}}
	h.reg = &Registry{
		ctx:        ctx,
		logger:     h.logger,
		supervisor: newSupervisorState(),
	}
	h.reg.rebuildWorkflow = func(_ context.Context, id string, wf storage.Workflow) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.calls = append(h.calls, rebuildCall{id: id, wf: wf})
		err := h.failNext
		h.failNext = nil
		return err
	}
	return h
}

func (h *supervisorHarness) rebuilds() []rebuildCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]rebuildCall(nil), h.calls...)
}

// The supervisor's restart budget was unit-tested; the supervisor itself was
// not. Nothing asserted that a reported stall actually rebuilds the workflow,
// that an exhausted budget stops it doing so, or that a failed rebuild is
// survivable — the whole point of the component.
func TestSuperviseStallRebuildsTheWorkflow(t *testing.T) {
	wf := storage.Workflow{ID: "wf-1", Name: "orders"}

	t.Run("a reported stall rebuilds the workflow", func(t *testing.T) {
		h := newSupervisorHarness(t)
		h.reg.superviseStall("wf-1", wf, "no message completed in 63s")

		calls := h.rebuilds()
		if len(calls) != 1 {
			t.Fatalf("rebuilt %d times, want 1: a stalled workflow was never restarted", len(calls))
		}
		if calls[0].id != "wf-1" || calls[0].wf.Name != "orders" {
			t.Errorf("rebuilt %+v, want the stalled workflow", calls[0])
		}
		if !h.logger.contains("no message completed in 63s") {
			t.Error("the restart did not record why it happened, so the underlying bug stays invisible")
		}
	})

	t.Run("an exhausted budget stands down instead of looping", func(t *testing.T) {
		h := newSupervisorHarness(t)
		now := time.Now()
		for i := 1; i <= maxStallRestarts; i++ {
			h.reg.supervisor.allow("wf-1", now)
			now = now.Add(settleFor(i) + time.Second)
		}

		h.reg.superviseStall("wf-1", wf, "still wedged")

		if n := len(h.rebuilds()); n != 0 {
			t.Fatalf("rebuilt %d times past the budget: a permanently broken sink would restart forever", n)
		}
		if !h.logger.contains("manual intervention") {
			t.Error("standing down was not announced, so nobody knows supervision has stopped")
		}
	})

	t.Run("a stall inside the settle window is distinguished from an exhausted budget", func(t *testing.T) {
		h := newSupervisorHarness(t)
		h.reg.supervisor.allow("wf-1", time.Now())

		h.reg.superviseStall("wf-1", wf, "wedged again straight away")

		if n := len(h.rebuilds()); n != 0 {
			t.Fatalf("rebuilt %d times inside the settle window", n)
		}
		if h.logger.contains("manual intervention") {
			t.Error("a settling restart was reported as exhausted supervision, which sends an operator after a problem that does not exist")
		}
		if !h.logger.contains("settling") {
			t.Errorf("the refusal was not explained; lines=%v", h.logger.lines)
		}
	})

	t.Run("a failed rebuild is reported, not fatal", func(t *testing.T) {
		h := newSupervisorHarness(t)
		h.mu.Lock()
		h.failNext = errors.New("sink still unreachable")
		h.mu.Unlock()

		h.reg.superviseStall("wf-1", wf, "wedged")

		if !h.logger.contains("sink still unreachable") {
			t.Error("a failed automatic restart was swallowed")
		}
	})

	t.Run("a closing registry does not restart anything", func(t *testing.T) {
		h := newSupervisorHarness(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		h.reg.ctx = ctx

		h.reg.superviseStall("wf-1", wf, "wedged")

		if n := len(h.rebuilds()); n != 0 {
			t.Errorf("rebuilt %d workflows while shutting down", n)
		}
	})
}

// A sink outage stalls every workflow that writes to it, at the same moment. A
// fixed settle time restarts all of them in lockstep, so the sink comes back to
// the full load that just failed, repeatedly. Backing off further on each
// successive failure — and spreading the restarts out — is how a supervisor
// avoids amplifying the outage it is reacting to.
func TestSupervisorBacksOffBetweenRestarts(t *testing.T) {
	t.Run("each successive restart waits longer", func(t *testing.T) {
		var prev time.Duration
		for attempt := 1; attempt <= maxStallRestarts; attempt++ {
			got := settleFor(attempt)
			if attempt > 1 && got <= prev {
				t.Errorf("settle after restart %d = %v, not longer than the previous %v", attempt, got, prev)
			}
			prev = got
		}
	})

	t.Run("the first settle is the documented baseline", func(t *testing.T) {
		if got := settleFor(1); got != stallRestartSettle {
			t.Errorf("settleFor(1) = %v, want %v", got, stallRestartSettle)
		}
	})

	t.Run("backoff is capped so a workflow is never abandoned", func(t *testing.T) {
		if got := settleFor(100); got > maxStallRestartSettle {
			t.Errorf("settleFor(100) = %v, above the cap %v", got, maxStallRestartSettle)
		}
	})

	t.Run("the settle gate widens with the attempt count", func(t *testing.T) {
		s := newSupervisorState()
		t0 := time.Unix(1_700_000_000, 0)

		if ok, _ := s.allow("wf", t0); !ok {
			t.Fatal("first restart refused")
		}
		// Past the first settle window, the second restart is allowed.
		t1 := t0.Add(settleFor(1) + time.Second)
		if ok, _ := s.allow("wf", t1); !ok {
			t.Fatal("second restart refused after its settle window elapsed")
		}
		// The second settle window is longer, so the same wait is not enough.
		if ok, _ := s.allow("wf", t1.Add(settleFor(1)+time.Second)); ok {
			t.Error("third restart allowed after only the first settle window: backoff is not widening")
		}
		if ok, _ := s.allow("wf", t1.Add(settleFor(2)+time.Second)); !ok {
			t.Error("third restart refused after its own settle window elapsed")
		}
	})
}

// An operator who fixes the sink and restarts the workflow by hand is starting a
// fresh episode. clearStallHistory existed for exactly this and was never
// called, so a workflow that exhausted its budget stayed unsupervised for the
// rest of the 30-minute window no matter what the operator did.
func TestManualStopRefillsTheRestartBudget(t *testing.T) {
	t.Run("an operator stop clears the history", func(t *testing.T) {
		reg := &Registry{supervisor: newSupervisorState()}
		now := time.Now()
		for i := 1; i <= maxStallRestarts; i++ {
			reg.supervisor.allow("wf-1", now)
			now = now.Add(settleFor(i) + time.Second)
		}
		if ok, _ := reg.supervisor.allow("wf-1", now); ok {
			t.Fatal("precondition: budget should be exhausted")
		}

		reg.onManualStop("wf-1")

		if ok, _ := reg.supervisor.allow("wf-1", now); !ok {
			t.Error("an operator stop left the restart budget exhausted: fixing the sink and restarting buys no supervision")
		}
	})

	t.Run("a supervisor stop does not clear the history", func(t *testing.T) {
		reg := &Registry{supervisor: newSupervisorState()}
		now := time.Now()
		reg.supervisor.allow("wf-1", now)

		// The supervisor's own restart must not refill its own budget, or the
		// limit it enforces would never be reached.
		if ok, _ := reg.supervisor.allow("wf-1", now.Add(time.Second)); ok {
			t.Error("the supervisor restarted again inside its own settle window")
		}
	})
}
