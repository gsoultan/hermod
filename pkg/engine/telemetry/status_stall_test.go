package telemetry

import (
	"sync"
	"testing"
)

// The background health check decides whether to publish "running" by reading
// the engine status and then writing it back — two separate lock acquisitions
// in pkg/engine/runner.go, at :443 and :473. The stall watchdog writes
// "stalled" between them often enough to matter: a wedged sink still answers
// Ping, so the health check's own guard sees a pre-stall value, concludes all
// is well, and publishes "running" over a stall that had already been reported
// to the supervisor.
//
// That is the failure stall_watchdog_test.go:115 names — "the UI would still
// show this workflow as healthy" — and it took a loaded CI runner to surface,
// because the window is only as wide as the gap between the two calls.
//
// A conditional write closes it: decide and publish under one lock.
func TestSetEngineStatusUnless(t *testing.T) {
	t.Run("writes when the current status is not excluded", func(t *testing.T) {
		s := NewStatusTracker()
		s.SetEngineStatus("connecting")

		if !s.SetEngineStatusUnless("running", "stalled") {
			t.Fatal("the write was refused from a status that is not excluded")
		}
		if _, _, got, _, _, _, _, _ := s.GetStatus(); got != "running" {
			t.Errorf("engine status = %q, want %q", got, "running")
		}
	})

	t.Run("refuses when the current status is excluded", func(t *testing.T) {
		s := NewStatusTracker()
		s.SetEngineStatus("stalled")

		if s.SetEngineStatusUnless("running", "stalled") {
			t.Error("the write was accepted over a stall")
		}
		if _, _, got, _, _, _, _, _ := s.GetStatus(); got != "stalled" {
			t.Errorf("engine status = %q, want %q: a reported stall was cleared", got, "stalled")
		}
	})

	// The point of the method. Read-then-write loses this race; deciding under
	// the same lock cannot. Run enough times that an unsynchronised
	// implementation is overwhelmingly likely to be caught.
	t.Run("a stall is never overwritten by a concurrent health check", func(t *testing.T) {
		for i := range 2000 {
			s := NewStatusTracker()
			s.SetEngineStatus("running")

			var wg sync.WaitGroup
			wg.Add(2)
			// The stall watchdog.
			go func() { defer wg.Done(); s.SetEngineStatus("stalled") }()
			// The background health check, publishing its verdict.
			go func() { defer wg.Done(); s.SetEngineStatusUnless("running", "stalled") }()
			wg.Wait()

			// Either order is legal, but only one end state is: once "stalled"
			// has been written it must still be there. The health check may win
			// the race and write "running" first — then the watchdog overwrites
			// it, which is correct. What must never happen is the reverse.
			_, _, got, _, _, _, _, _ := s.GetStatus()
			if got != "stalled" {
				t.Fatalf("iteration %d: engine status = %q, want %q: the health check cleared a reported stall", i, got, "stalled")
			}
		}
	})
}
