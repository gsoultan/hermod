package engine

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	hermod "github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/pkg/comm/buffer"
)

// retainingIdleSource reports growing replication lag while holding nothing the
// pipeline has not finished — the normal state of a healthy, idle CDC workflow.
//
// GetLag measures pg_current_wal_lsn() against the slot's confirmed_flush_lsn.
// pg_current_wal_lsn() is instance-wide, so every write anywhere on the source
// server advances it, while confirmed_flush_lsn only advances when Hermod
// acknowledges a message it was actually sent
// (pkg/comm/source/postgres/postgres.go:1724 — keepalives never advance it).
// An idle workflow on a busy server therefore reports lag that grows without
// limit and never returns to zero.
type retainingIdleSource struct {
	lag     atomic.Uint64
	pending atomic.Bool
}

func (s *retainingIdleSource) Read(ctx context.Context) (hermod.Message, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (s *retainingIdleSource) Ack(context.Context, hermod.Message) error { return nil }
func (s *retainingIdleSource) Ping(context.Context) error                { return nil }
func (s *retainingIdleSource) Close() error                              { return nil }

func (s *retainingIdleSource) GetLag(context.Context) (uint64, error) {
	// Growing, as it would on any server with other write activity.
	return s.lag.Add(1 << 20), nil
}

func (s *retainingIdleSource) PendingWork() (bool, bool) { return s.pending.Load(), true }

// Retained WAL is not the same thing as outstanding work, and treating it as
// such turns automatic recovery into an automatic outage: an idle workflow whose
// source database merely has other traffic would be declared stalled after the
// threshold, restarted three times, and then reported as needing manual
// intervention — while nothing was ever wrong with it.
func TestGrowingLagAloneIsNotAStall(t *testing.T) {
	t.Run("an idle source retaining WAL is left alone", func(t *testing.T) {
		src := &retainingIdleSource{}
		src.pending.Store(false)

		eng := NewEngine(src, []hermod.Sink{&mockSink{}}, buffer.NewRingBuffer(64))
		eng.SetConfig(stallTestConfig(500 * time.Millisecond))
		eng.SetWorkflowID("wf-idle-retaining")

		stalled := make(chan string, 1)
		eng.SetOnStall(func(reason string) {
			select {
			case stalled <- reason:
			default:
			}
		})

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go func() { _ = eng.Start(ctx) }()

		select {
		case reason := <-stalled:
			t.Fatalf("a healthy idle workflow was restarted because its source database had unrelated traffic: %s", reason)
		case <-time.After(4 * time.Second):
		}
	})

	t.Run("a source actually holding un-acknowledged work is reported", func(t *testing.T) {
		src := &retainingIdleSource{}
		src.pending.Store(true)

		eng := NewEngine(src, []hermod.Sink{&mockSink{}}, buffer.NewRingBuffer(64))
		eng.SetConfig(stallTestConfig(time.Second))
		eng.SetWorkflowID("wf-really-wedged")

		stalled := make(chan string, 1)
		eng.SetOnStall(func(reason string) {
			select {
			case stalled <- reason:
			default:
			}
		})

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go func() { _ = eng.Start(ctx) }()

		select {
		case <-stalled:
		case <-time.After(15 * time.Second):
			t.Fatal("a source holding messages it handed over and never got acknowledged was not reported")
		}
	})

	t.Run("a source that cannot answer still falls back to lag", func(t *testing.T) {
		// Every source runs wrapped in a decorator that implements
		// PendingWorkReporter on the wrapped source's behalf. If "I cannot tell"
		// were reported as "nothing is outstanding", wrapping a non-CDC source
		// would silently disable the lag fallback and hide its wedges.
		src := &unknownPendingSource{}
		src.lag.Store(8 << 20)

		eng := NewEngine(src, []hermod.Sink{&mockSink{}}, buffer.NewRingBuffer(64))
		eng.SetConfig(stallTestConfig(time.Second))
		eng.SetWorkflowID("wf-unknown-pending")

		stalled := make(chan string, 1)
		eng.SetOnStall(func(reason string) {
			select {
			case stalled <- reason:
			default:
			}
		})

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go func() { _ = eng.Start(ctx) }()

		select {
		case <-stalled:
		case <-time.After(15 * time.Second):
			t.Fatal("a wrapped source that cannot report pending work lost its lag fallback and its wedge went unreported")
		}
	})
}

// unknownPendingSource reports lag but cannot say whether it is owed
// acknowledgements — the shape of any non-CDC source seen through a decorator.
type unknownPendingSource struct {
	lag atomic.Uint64
}

func (s *unknownPendingSource) Read(ctx context.Context) (hermod.Message, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (s *unknownPendingSource) Ack(context.Context, hermod.Message) error { return nil }
func (s *unknownPendingSource) Ping(context.Context) error                { return nil }
func (s *unknownPendingSource) Close() error                              { return nil }
func (s *unknownPendingSource) GetLag(context.Context) (uint64, error)    { return s.lag.Load(), nil }
func (s *unknownPendingSource) PendingWork() (bool, bool)                 { return false, false }
