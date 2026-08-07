package registry

import (
	"fmt"
	"os"
	"testing"

	"github.com/user/hermod/pkg/comm/message"
)

// TestMain enforces the message ownership contract across every test in this
// package.
//
// Messages are pooled and reference-counted by hand: roughly three dozen
// Retain/Release sites spread over the traversal, the node executors and the
// writer. Releasing one time too many hands the message back to the pool while
// an owner still holds it; the pool then refills it for a different message and
// the stale owner delivers the wrong payload. The observable symptom is not an
// error — it is some messages delivered twice, others never delivered, and the
// totals still adding up. That is how a data-loss bug lived in
// traversal.runNode's source branch without a single failing test.
//
// message.OverReleaseCount is the tripwire: it counts every Release that
// arrives after a refcount already reached zero. Any non-zero value is a
// correctness bug somewhere in this package's call graph, so the whole package
// fails rather than leaving it to be discovered in production.
func TestMain(m *testing.M) {
	message.ResetOverReleaseCount()

	code := m.Run()

	if n := message.OverReleaseCount(); n != 0 && code == 0 {
		fmt.Fprintf(os.Stderr,
			"\nFAIL: %d message over-release(s) during this package's tests.\n"+
				"A message was released after its refcount reached zero, so it went back to the\n"+
				"pool while still referenced. Expect duplicated and lost messages, not an error.\n"+
				"Check every Retain/Release pair on the paths the failing tests exercised.\n", n)
		code = 1
	}

	os.Exit(code)
}
