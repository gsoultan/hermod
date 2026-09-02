package source

import (
	"context"
	"errors"
	"testing"

	"github.com/gsoultan/hermod"
)

// lagAwareSource is a primary source that reports replication lag, runs a deep
// readiness check, and accepts a logger — the three optional interfaces a real
// CDC source (pkg/comm/source/postgres) implements.
type lagAwareSource struct {
	priorityMockSource
	lag        uint64
	lagErr     error
	readyErr   error
	readyCalls int
	loggerSet  hermod.Logger
	lagCalls   int
}

func (s *lagAwareSource) GetLag(context.Context) (uint64, error) {
	s.lagCalls++
	return s.lag, s.lagErr
}

func (s *lagAwareSource) IsReady(context.Context) error {
	s.readyCalls++
	return s.readyErr
}

func (s *lagAwareSource) SetLogger(l hermod.Logger) { s.loggerSet = l }

type discardLogger struct{}

func (discardLogger) Debug(string, ...any) {}
func (discardLogger) Info(string, ...any)  {}
func (discardLogger) Warn(string, ...any)  {}
func (discardLogger) Error(string, ...any) {}

// Wrapping a source must not hide what the source can tell you.
//
// MetricsSource learned this the hard way: embedding hermod.Source does not
// promote methods the interface does not declare, so wrapping a PostgresSource
// silently hid GetLag and every lag-based check downstream read zero
// (pkg/comm/source/decorators.go:120-133). PrioritySource has the same shape and
// was never fixed, so enabling PrioritizeDLQ re-opened exactly that hole: the
// stall watchdog asks e.source for its lag (pkg/engine/stall.go:165), the type
// assertion fails against a PrioritySource, and a pipeline retaining WAL looks
// completely idle — which is the one condition the watchdog exists to catch.
func TestPrioritySourceForwardsOptionalInterfaces(t *testing.T) {
	newPair := func() (*lagAwareSource, *PrioritySource) {
		primary := &lagAwareSource{}
		recovery := &priorityMockSource{}
		return primary, NewPrioritySource(recovery, primary, nil)
	}

	t.Run("reports the primary source's lag", func(t *testing.T) {
		primary, ps := newPair()
		primary.lag = 21 * 1024 * 1024

		lr, ok := any(ps).(hermod.LagReporter)
		if !ok {
			t.Fatal("PrioritySource does not implement hermod.LagReporter: the stall watchdog is blind whenever PrioritizeDLQ is on")
		}
		lag, err := lr.GetLag(context.Background())
		if err != nil {
			t.Fatalf("GetLag: %v", err)
		}
		if lag != primary.lag {
			t.Errorf("lag = %d, want %d (retained WAL reported as no outstanding work)", lag, primary.lag)
		}
	})

	t.Run("propagates a lag error rather than reporting zero", func(t *testing.T) {
		primary, ps := newPair()
		primary.lagErr = errors.New("slot query failed")

		lr := any(ps).(hermod.LagReporter)
		if _, err := lr.GetLag(context.Background()); err == nil {
			t.Error("a failed lag query was reported as zero lag, which reads as a healthy idle pipeline")
		}
	})

	t.Run("a primary that cannot report lag is not an error", func(t *testing.T) {
		ps := NewPrioritySource(&priorityMockSource{}, &priorityMockSource{}, nil)
		lr, ok := any(ps).(hermod.LagReporter)
		if !ok {
			t.Fatal("PrioritySource does not implement hermod.LagReporter")
		}
		lag, err := lr.GetLag(context.Background())
		if err != nil || lag != 0 {
			t.Errorf("GetLag = (%d, %v), want (0, nil) for a source with no lag concept", lag, err)
		}
	})

	t.Run("runs the primary's deep readiness check", func(t *testing.T) {
		primary, ps := newPair()
		primary.readyErr = errors.New("replication slot is inactive")

		rc, ok := any(ps).(hermod.ReadyChecker)
		if !ok {
			t.Fatal("PrioritySource does not implement hermod.ReadyChecker: health checks fall back to a shallow Ping that passes while CDC is dead")
		}
		if err := rc.IsReady(context.Background()); err == nil {
			t.Error("IsReady reported healthy while the primary's slot was inactive")
		}
		if primary.readyCalls == 0 {
			t.Error("the primary's readiness check was never invoked")
		}
	})

	t.Run("forwards the logger to both wrapped sources", func(t *testing.T) {
		primary, ps := newPair()
		l, ok := any(ps).(hermod.Loggable)
		if !ok {
			t.Fatal("PrioritySource does not implement hermod.Loggable: a DLQ-prioritized source stops logging entirely")
		}
		lg := discardLogger{}
		l.SetLogger(lg)
		if primary.loggerSet == nil {
			t.Error("the primary source never received the logger")
		}
		if ps.logger == nil {
			t.Error("PrioritySource's own logger was not set, so its DLQ decisions stay unlogged")
		}
	})
}
