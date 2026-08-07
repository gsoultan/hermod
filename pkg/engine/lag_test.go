package engine

import "testing"

// A replication slot that stops advancing retains WAL on the source database.
// During the wedge this reached 21 MB and was still climbing, with nothing
// logged: the failure mode is that Hermod quietly fills someone else's primary.
// The threshold has to be crossing-triggered, or a slot parked above the line
// writes a log line per health check for as long as it stays there.
func TestLagStateReportsCrossingsNotLevels(t *testing.T) {
	const threshold = 1000

	t.Run("below the threshold is silent", func(t *testing.T) {
		var l lagState
		for _, lag := range []uint64{0, 1, 999} {
			if b, c := l.observe(lag, threshold); b || c {
				t.Fatalf("lag %d reported a crossing", lag)
			}
		}
	})

	t.Run("reports once on the way up", func(t *testing.T) {
		var l lagState
		if b, _ := l.observe(1000, threshold); !b {
			t.Fatal("did not report reaching the threshold")
		}
		for _, lag := range []uint64{1200, 5000, 20000} {
			if b, _ := l.observe(lag, threshold); b {
				t.Fatalf("re-reported at lag %d; logs would flood", lag)
			}
		}
	})

	t.Run("clears with hysteresis, not at the line", func(t *testing.T) {
		var l lagState
		l.observe(2000, threshold)
		// Still above half: a slot hovering here must not flap.
		if _, c := l.observe(600, threshold); c {
			t.Fatal("cleared while still above half the threshold")
		}
		if _, c := l.observe(499, threshold); !c {
			t.Fatal("did not clear after dropping well below the threshold")
		}
		// And it can fire again on a genuine second breach.
		if b, _ := l.observe(1000, threshold); !b {
			t.Fatal("did not report a second breach")
		}
	})

	t.Run("a zero threshold disables the check", func(t *testing.T) {
		var l lagState
		if b, c := l.observe(1<<40, 0); b || c {
			t.Fatal("reported with the check disabled")
		}
	})
}
