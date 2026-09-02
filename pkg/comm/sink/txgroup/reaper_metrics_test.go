package txgroup

import (
	"context"
	"testing"

	"github.com/gsoultan/Hermod/pkg/engine/telemetry"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// Transactions left in doubt, reported rather than left for somebody to notice.
//
// A prepared transaction whose outcome is unresolved is the most expensive state
// this system can be in. On PostgreSQL it holds locks and blocks VACUUM
// cluster-wide — not just for the table involved, and not just for Hermod — for
// as long as it lasts. The coordinator has always been able to count them
// (Coordinator.InDoubt) and the reaper has always swept them, but neither
// published anything: the documented backstop was a human remembering to run
//
//	SELECT * FROM pg_prepared_xacts;
//
// against the destination. That is a check nobody runs on a good day, which
// makes it a check that is not running on the bad one.
//
// The reaper already wakes on a ticker and already holds the coordinator, so it
// is where the numbers come from.

func TestReapingPublishesTheInDoubtCount(t *testing.T) {
	a := &txSink{name: "a"}
	b := &txSink{name: "b"}
	g := newGroup(t, Member{ID: "a", Sink: a}, Member{ID: "b", Sink: b})

	wf := g.coordinator.WorkflowID()

	// Seeded with a value the sweep must overwrite. Starting from zero would
	// let this pass without anything being published at all, because an unset
	// gauge also reads zero — the assertion has to be that the sweep *writes*,
	// not merely that the number looks right.
	telemetry.TxGroupInDoubt.WithLabelValues(wf).Set(99)

	// A sweep with nothing outstanding still has to publish, or the gauge holds
	// whatever it last saw and an alert stays lit after the problem is gone.
	g.reapOnce(context.Background())

	if got := testutil.ToFloat64(telemetry.TxGroupInDoubt.WithLabelValues(wf)); got != 0 {
		t.Errorf("in-doubt gauge reads %v after a clean sweep, want 0\n"+
			"a gauge that is only written when something is wrong never comes back down, "+
			"so the alert it drives cannot clear", got)
	}
}

// The count has to be published even when the sweep reaps nothing, because a
// transaction can be in doubt and not yet past its deadline — which is exactly
// the window where somebody would want to know.
func TestTheInDoubtGaugeReflectsOutstandingTransactions(t *testing.T) {
	a := &txSink{name: "a"}
	b := &txSink{name: "b"}
	g := newGroup(t, Member{ID: "a", Sink: a}, Member{ID: "b", Sink: b})

	wf := g.coordinator.WorkflowID()
	telemetry.TxGroupInDoubt.WithLabelValues(wf).Set(99)

	ctx := context.Background()

	// A write that prepares and commits leaves nothing behind.
	if err := g.WriteBatch(ctx, nil); err != nil {
		t.Fatalf("empty batch: %v", err)
	}
	g.reapOnce(ctx)

	if got := testutil.ToFloat64(telemetry.TxGroupInDoubt.WithLabelValues(wf)); got != 0 {
		t.Errorf("in-doubt gauge reads %v after a completed write, want 0", got)
	}

	// Whatever the coordinator reports is what must be published; asserting
	// against it rather than a literal keeps this honest if the group's
	// bookkeeping changes.
	want, err := g.InDoubt(ctx)
	if err != nil {
		t.Fatalf("InDoubt: %v", err)
	}
	g.reapOnce(ctx)
	if got := testutil.ToFloat64(telemetry.TxGroupInDoubt.WithLabelValues(wf)); got != float64(want) {
		t.Errorf("in-doubt gauge reads %v, coordinator reports %d", got, want)
	}
}
