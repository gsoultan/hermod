package engine

import (
	"testing"
	"time"
)

// A wedged pipeline is silent. Observed twice, reproducibly: after a sink
// outage the source kept its replication connection alive and its received LSN
// advancing, but no message reached a sink again — for minutes, then
// indefinitely. The workflow still reported active=true and "running", nothing
// was logged at any level, and the replication slot accumulated 21 MB of WAL.
// Only a manual workflow restart flushed it (200/200 + 5/5 delivered at once).
//
// The distinction that matters is idle vs wedged: a pipeline with nothing to do
// is healthy, a pipeline with work outstanding and no progress is not.
func TestStallStateDistinguishesIdleFromWedged(t *testing.T) {
	const threshold = 30 * time.Second
	t0 := time.Unix(1_700_000_000, 0)

	t.Run("idle with no work pending is never a stall", func(t *testing.T) {
		s := newStallState(t0)
		for i := range 10 {
			now := t0.Add(time.Duration(i) * time.Minute)
			if stalled, _ := s.observe(0, false, now, threshold); stalled {
				t.Fatalf("reported a stall at %v while no work was pending", now.Sub(t0))
			}
		}
	})

	t.Run("work pending and no progress past the threshold is a stall", func(t *testing.T) {
		s := newStallState(t0)
		s.observe(5, true, t0, threshold) // baseline: 5 delivered, clock starts
		if stalled, _ := s.observe(5, true, t0.Add(10*time.Second), threshold); stalled {
			t.Fatal("reported a stall before the threshold elapsed")
		}
		stalled, _ := s.observe(5, true, t0.Add(31*time.Second), threshold)
		if !stalled {
			t.Fatal("did not report a stall after work sat unprocessed past the threshold")
		}
	})

	t.Run("progress resets the clock", func(t *testing.T) {
		s := newStallState(t0)
		s.observe(5, true, t0.Add(20*time.Second), threshold)
		// A message got through: the pipeline is moving again.
		s.observe(6, true, t0.Add(25*time.Second), threshold)
		if stalled, _ := s.observe(6, true, t0.Add(50*time.Second), threshold); stalled {
			t.Fatal("stalled too early: progress at 25s should have reset the clock")
		}
		if stalled, _ := s.observe(6, true, t0.Add(56*time.Second), threshold); !stalled {
			t.Fatal("did not stall 31s after the last progress")
		}
	})

	t.Run("a stall is reported once, not on every tick", func(t *testing.T) {
		s := newStallState(t0)
		s.observe(1, true, t0, threshold) // baseline
		if stalled, _ := s.observe(1, true, t0.Add(31*time.Second), threshold); !stalled {
			t.Fatal("precondition: expected the first stall report")
		}
		for i := range 5 {
			if stalled, _ := s.observe(1, true, t0.Add(time.Duration(40+i)*time.Second), threshold); stalled {
				t.Fatal("re-reported the same stall; logs would flood")
			}
		}
	})

	t.Run("recovery is reported once progress resumes", func(t *testing.T) {
		s := newStallState(t0)
		s.observe(1, true, t0, threshold) // baseline
		if stalled, _ := s.observe(1, true, t0.Add(31*time.Second), threshold); !stalled {
			t.Fatal("precondition: expected a stall before testing recovery")
		}
		_, recovered := s.observe(2, true, t0.Add(40*time.Second), threshold)
		if !recovered {
			t.Fatal("did not report recovery after progress resumed")
		}
		if _, again := s.observe(3, true, t0.Add(50*time.Second), threshold); again {
			t.Fatal("re-reported recovery")
		}
	})
}
