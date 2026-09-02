package engine

import (
	"fmt"
	"os"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
)

// TestMain enforces both reference-counting contracts this package depends on.
//
// A message is pooled and hand-refcounted; a pendingMessage wraps it for the
// sink writers and is refcounted separately. Over-releasing either one returns
// a live object to its pool, where it is handed to a second owner. Neither
// failure raises an error: messages get duplicated and lost while the totals
// still balance, which is exactly how such a bug survives a green test suite.
//
// Both counters must be zero at the end of the package's tests.
func TestMain(m *testing.M) {
	message.ResetOverReleaseCount()
	before := PendingOverReleaseCount()

	code := m.Run()

	if code == 0 {
		if n := message.OverReleaseCount(); n != 0 {
			fmt.Fprintf(os.Stderr,
				"\nFAIL: %d message over-release(s) during this package's tests.\n"+
					"A message returned to the pool while still referenced; expect duplicated and\n"+
					"lost messages rather than an error.\n", n)
			code = 1
		}
		// Tests that deliberately over-release to exercise the idempotent guard
		// declare their budget via allowPendingOverReleases; anything beyond it
		// is unaccounted for and therefore a bug.
		if n := PendingOverReleaseCount() - before - expectedPendingOverReleases.Load(); n != 0 {
			fmt.Fprintf(os.Stderr,
				"\nFAIL: %d undeclared pendingMessage over-release(s) during this package's tests.\n"+
					"Two owners can now share one pendingMessage and its single-slot done\n"+
					"channel, so one message's completion is never signalled. If a test does this\n"+
					"on purpose, it must declare the count with allowPendingOverReleases.\n", n)
			code = 1
		}
	}

	os.Exit(code)
}
