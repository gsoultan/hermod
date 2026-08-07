package registry

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/user/hermod/internal/storage"
)

// Restart-storm limits. A workflow that stalls because its sink is genuinely
// gone will stall again straight after a restart, so the supervisor must give
// up rather than rebuild the engine forever.
const (
	// maxStallRestarts is how many automatic restarts are allowed inside
	// stallRestartWindow before the supervisor stands down.
	maxStallRestarts = 3
	// stallRestartWindow is the rolling window those restarts are counted in.
	stallRestartWindow = 30 * time.Minute
	// stallRestartSettle is how long a rebuilt engine is left alone before
	// another stall is acted on, so one episode cannot trigger a burst. It is
	// the base of an exponential backoff: see settleFor.
	stallRestartSettle = 2 * time.Minute
	// maxStallRestartSettle caps that backoff. Without a cap the wait would
	// outgrow stallRestartWindow, and a workflow would stop being supervised
	// altogether rather than being supervised less eagerly.
	maxStallRestartSettle = 8 * time.Minute
	// stallRestartJitter is the maximum extra delay added before a rebuild. A
	// shared sink stalls every workflow that writes to it at the same instant,
	// and restarting them in lockstep returns the full failed load to a sink
	// that is still recovering. Spreading them out costs seconds and avoids
	// re-creating the outage being recovered from.
	stallRestartJitter = 5 * time.Second
)

// settleFor returns how long to leave a rebuilt engine alone after its nth
// automatic restart.
//
// The wait doubles each time and is capped. A workflow that stalls once and
// recovers is back under supervision within minutes; one that stalls repeatedly
// because its sink is genuinely gone is retried progressively less often instead
// of at a fixed rate, which is the same shape a process supervisor or a
// CrashLoopBackOff uses for the same reason.
func settleFor(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	settle := stallRestartSettle
	for range attempt - 1 {
		settle *= 2
		if settle >= maxStallRestartSettle {
			return maxStallRestartSettle
		}
	}
	return settle
}

type stallRecord struct {
	attempts []time.Time
	last     time.Time
}

// supervisorState tracks restart history per workflow.
type supervisorState struct {
	mu      sync.Mutex
	records map[string]*stallRecord
}

func newSupervisorState() *supervisorState {
	return &supervisorState{records: make(map[string]*stallRecord)}
}

// allow reports whether a stalled workflow may be restarted now, and how many
// restarts have been used in the current window.
func (s *supervisorState) allow(id string, now time.Time) (ok bool, used int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec := s.records[id]
	if rec == nil {
		rec = &stallRecord{}
		s.records[id] = rec
	}

	// Ignore a stall reported while the previous restart is still settling. The
	// window widens with each restart, so a workflow that keeps wedging is
	// rebuilt progressively less often rather than on a fixed cadence.
	if !rec.last.IsZero() && now.Sub(rec.last) < settleFor(len(rec.attempts)) {
		return false, len(rec.attempts)
	}

	kept := rec.attempts[:0]
	for _, t := range rec.attempts {
		if now.Sub(t) < stallRestartWindow {
			kept = append(kept, t)
		}
	}
	rec.attempts = kept

	if len(rec.attempts) >= maxStallRestarts {
		return false, len(rec.attempts)
	}

	rec.attempts = append(rec.attempts, now)
	rec.last = now
	return true, len(rec.attempts)
}

// clearStallHistory forgets a workflow's restart history, so a workflow that is
// stopped and started by hand starts from a clean budget.
func (s *supervisorState) clearStallHistory(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.records, id)
}

// superviseStall rebuilds a wedged workflow.
//
// A stalled pipeline holds work it has stopped completing. Every cause found so
// far — a pooled message returned to the pool twice, a circuit breaker
// deadlocking against the status reader, a source that retired itself without
// saying so — presented identically from the outside and was cured identically:
// rebuild the engine. Nothing is lost by doing so, because an un-acknowledged
// replication slot replays from its confirmed position, which is why a manual
// restart always recovered every message.
//
// So the supervisor does what an operator was previously paged to do, and says
// so loudly. It is a backstop, not a licence to stop fixing the causes: each
// restart is logged as an error precisely so the underlying bug stays visible.
func (r *Registry) superviseStall(id string, wf storage.Workflow, reason string) {
	if r.ctx.Err() != nil {
		return
	}

	ok, used := r.supervisor.allow(id, time.Now())
	if !ok {
		// Two different refusals, and an operator needs to tell them apart: a
		// spent budget means supervision has stopped and someone has to act, a
		// settle window means the last rebuild is still being given its chance.
		if used >= maxStallRestarts {
			r.logger.Error("Workflow stalled but automatic recovery is exhausted; manual intervention required",
				"workflow_id", id,
				"reason", reason,
				"restarts_in_window", used,
				"window", stallRestartWindow.String(),
				"hint", "the sink is probably still unreachable; fix it, then stop and start the workflow to restore supervision")
			return
		}
		r.logger.Warn("Workflow stalled again while the last automatic restart was still settling; leaving it alone for now",
			"workflow_id", id,
			"reason", reason,
			"restarts_in_window", used,
			"settle", settleFor(used).String())
		return
	}

	r.logger.Error("Workflow stalled; restarting it automatically",
		"workflow_id", id,
		"reason", reason,
		"attempt", used,
		"max_attempts", maxStallRestarts,
		"note", "no data is lost: un-acknowledged changes replay from the source")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := r.rebuild(ctx, id, wf); err != nil {
		r.logger.Error("Stalled workflow could not be restarted automatically",
			"workflow_id", id,
			"attempt", used,
			"error", err,
			"hint", "the workflow is stopped; fix the underlying fault and start it again")
		return
	}

	r.logger.Info("Stalled workflow restarted", "workflow_id", id, "attempt", used)
}

// rebuild stops a workflow's engine and starts it again, through the seam that
// lets the supervisor's decisions be tested without standing up storage.
func (r *Registry) rebuild(ctx context.Context, id string, wf storage.Workflow) error {
	if fn := r.rebuildWorkflow; fn != nil {
		return fn(ctx, id, wf)
	}
	return r.restartWorkflowEngine(ctx, id, wf)
}

// restartWorkflowEngine is the real rebuild: stop the wedged engine, then start
// a fresh one from the same definition.
//
// Nothing is lost by doing so, because an un-acknowledged replication slot
// replays from its confirmed position — which is why a manual restart always
// recovered every message.
func (r *Registry) restartWorkflowEngine(ctx context.Context, id string, wf storage.Workflow) error {
	// Spread simultaneous restarts out: a shared sink stalls every workflow
	// writing to it at the same instant, and rebuilding them in lockstep hands
	// the recovering sink exactly the load that just failed.
	if delay := time.Duration(rand.Int64N(int64(stallRestartJitter) + 1)); delay > 0 {
		select {
		case <-r.ctx.Done():
			return r.ctx.Err()
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}

	if err := r.StopEngineWithoutUpdate(ctx, id); err != nil {
		return fmt.Errorf("stop stalled workflow: %w", err)
	}
	if err := r.StartWorkflow(id, wf); err != nil {
		return fmt.Errorf("start stalled workflow: %w", err)
	}
	return nil
}

// onManualStop resets a workflow's automatic-restart budget.
//
// An operator stopping a workflow is ending the current episode: whatever they
// do next — fix the sink, change the config, start it again — begins a new one.
// Carrying the old episode's exhausted budget forward meant that a workflow
// which had used its three restarts got no supervision at all for the rest of
// the 30-minute window, however thoroughly the fault had been fixed.
func (r *Registry) onManualStop(id string) {
	if r.supervisor != nil {
		r.supervisor.clearStallHistory(id)
	}
}
