package txgroup

import (
	"context"
	"time"

	"github.com/gsoultan/Hermod/pkg/engine/telemetry"
)

// DefaultReapInterval is how often StartReaper sweeps for transactions left in
// doubt. Comfortably shorter than the coordinator's MaxPreparedAge so a stale
// transaction is caught within roughly one extra interval of passing its
// deadline, rather than one extra deadline.
const DefaultReapInterval = time.Minute

// StartReaper runs Reap on a ticker until ctx is done, returning a function
// that stops it and waits for the sweep in flight.
//
// This exists because Recover only runs at start-up, and the failure it does
// not cover is the one that hurts: a coordinator that prepares, dies, and is
// never restarted. Its participants stay in doubt indefinitely — on PostgreSQL
// that means locks held and VACUUM blocked cluster-wide, for as long as nobody
// notices. A group with no reaper running is a group whose safety net only
// catches you if you reboot.
//
// interval <= 0 uses DefaultReapInterval.
//
// Errors are logged rather than returned: a sweep that fails because the store
// is briefly unavailable should be retried on the next tick, not tear anything
// down.
func (s *Sink) StartReaper(ctx context.Context, interval time.Duration) (stop func()) {
	if interval <= 0 {
		interval = DefaultReapInterval
	}

	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.reapOnce(ctx)
			}
		}
	}()

	return func() {
		cancel()
		<-done
	}
}

// reapOnce performs one sweep, logging and publishing what it found.
func (s *Sink) reapOnce(ctx context.Context) {
	workflowID := s.coordinator.WorkflowID()

	reaped, err := s.Reap(ctx)
	if err != nil {
		if s.logger != nil {
			s.logger.Error("txgroup: reap sweep failed; transactions may still be in doubt",
				"error", err)
		}
		// Deliberately no gauge update here. A sweep that could not read the
		// store knows nothing about the current count, and writing a stale or
		// zero value would clear an alert while the condition it reports is
		// still true — worse than leaving the last known number in place.
		return
	}
	if reaped > 0 {
		telemetry.TxGroupReaped.WithLabelValues(workflowID).Add(float64(reaped))
		if s.logger != nil {
			// Worth a warning rather than an info: reaping is a correctness
			// backstop, and a non-zero count means something upstream failed to
			// resolve its own transaction.
			s.logger.Warn("txgroup: rolled back transactions left in doubt past their deadline",
				"count", reaped)
		}
	}

	// Published on every successful sweep, including the ones that find
	// nothing. A gauge only written when something is wrong never comes back
	// down, and the alert it drives cannot clear.
	//
	// This is the number that matters: a transaction in doubt but not yet past
	// its deadline is invisible to the reaping count above, and on PostgreSQL
	// it is already holding locks and blocking VACUUM cluster-wide.
	if n, err := s.InDoubt(ctx); err == nil {
		telemetry.TxGroupInDoubt.WithLabelValues(workflowID).Set(float64(n))
	} else if s.logger != nil {
		s.logger.Error("txgroup: could not count transactions in doubt; the gauge is stale",
			"error", err)
	}
}
