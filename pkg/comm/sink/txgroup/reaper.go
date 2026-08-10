package txgroup

import (
	"context"
	"time"
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

// reapOnce performs one sweep, logging what it found.
func (s *Sink) reapOnce(ctx context.Context) {
	reaped, err := s.Reap(ctx)
	if err != nil {
		if s.logger != nil {
			s.logger.Error("txgroup: reap sweep failed; transactions may still be in doubt",
				"error", err)
		}
		return
	}
	if reaped > 0 && s.logger != nil {
		// Worth a warning rather than an info: reaping is a correctness
		// backstop, and a non-zero count means something upstream failed to
		// resolve its own transaction.
		s.logger.Warn("txgroup: rolled back transactions left in doubt past their deadline",
			"count", reaped)
	}
}
