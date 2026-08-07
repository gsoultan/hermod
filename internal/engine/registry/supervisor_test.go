package registry

import (
	"testing"
	"time"
)

// The supervisor restarts a wedged workflow, so its restraint matters as much as
// its willingness: a sink that is genuinely gone will stall again immediately
// after every restart, and rebuilding the engine in a tight loop would turn one
// broken sink into a busy loop plus a flooded log.
func TestSupervisorRestartBudget(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0)

	t.Run("allows up to the limit, then stands down", func(t *testing.T) {
		s := newSupervisorState()
		now := t0
		for i := 1; i <= maxStallRestarts; i++ {
			ok, used := s.allow("wf", now)
			if !ok {
				t.Fatalf("restart %d refused, want allowed", i)
			}
			if used != i {
				t.Errorf("attempt count = %d, want %d", used, i)
			}
			now = now.Add(settleFor(i) + time.Second)
		}
		if ok, _ := s.allow("wf", now); ok {
			t.Fatal("restarted past the budget: a permanently broken sink would loop forever")
		}
	})

	t.Run("ignores a stall reported while the last restart is still settling", func(t *testing.T) {
		s := newSupervisorState()
		if ok, _ := s.allow("wf", t0); !ok {
			t.Fatal("first restart refused")
		}
		if ok, _ := s.allow("wf", t0.Add(stallRestartSettle/2)); ok {
			t.Fatal("restarted again before the rebuilt engine had a chance to settle")
		}
	})

	t.Run("the budget refills once the window passes", func(t *testing.T) {
		s := newSupervisorState()
		now := t0
		for i := 1; i <= maxStallRestarts; i++ {
			s.allow("wf", now)
			now = now.Add(settleFor(i) + time.Second)
		}
		if ok, _ := s.allow("wf", now); ok {
			t.Fatal("precondition: budget should be exhausted")
		}
		if ok, _ := s.allow("wf", now.Add(stallRestartWindow+time.Minute)); !ok {
			t.Fatal("budget never refilled; a workflow that stalls once a day would stop being supervised")
		}
	})

	t.Run("workflows have independent budgets", func(t *testing.T) {
		s := newSupervisorState()
		now := t0
		for i := 1; i <= maxStallRestarts; i++ {
			s.allow("wf-a", now)
			now = now.Add(settleFor(i) + time.Second)
		}
		if ok, _ := s.allow("wf-b", now); !ok {
			t.Fatal("one workflow's restarts consumed another's budget")
		}
	})

	t.Run("a manual restart clears the history", func(t *testing.T) {
		s := newSupervisorState()
		now := t0
		for i := 1; i <= maxStallRestarts; i++ {
			s.allow("wf", now)
			now = now.Add(settleFor(i) + time.Second)
		}
		s.clearStallHistory("wf")
		if ok, _ := s.allow("wf", now); !ok {
			t.Fatal("history not cleared: an operator fixing the sink and restarting gets no supervision")
		}
	})
}
