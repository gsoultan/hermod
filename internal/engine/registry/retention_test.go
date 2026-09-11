package registry

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/testutil"
	"github.com/gsoultan/hermod/pkg/engine/telemetry"
)

// --- Retention that silently did nothing -------------------------------------
//
// message_trace_steps stores before_data and after_data — the whole payload,
// twice, per node, per message. It is the fastest-growing table Hermod owns,
// and the only thing standing between it and the disk is this sweep.
//
// purgeRetention parsed the configured window with time.ParseDuration, which
// has no "d" unit. The UI defaults the field to "7d"
// (ui/.../useWorkflowStore.ts), so the parse failed on the default value, the
// call site was `if err == nil`, and the purge was skipped — silently, on every
// workflow, forever. Found on a live deployment whose PostgreSQL grew 50 GB in
// a couple of hours.
//
// A day-aware parseDuration already existed in this same file.

// retentionStorage records what the sweep asked to be deleted.
type retentionStorage struct {
	testutil.BaseMockStorage

	mu           sync.Mutex
	workflows    []storage.Workflow
	tracesBefore []time.Time
	auditsBefore []time.Time
}

func (s *retentionStorage) ListWorkflows(context.Context, storage.CommonFilter) ([]storage.Workflow, int, error) {
	return s.workflows, len(s.workflows), nil
}

func (s *retentionStorage) PurgeMessageTraces(_ context.Context, before time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tracesBefore = append(s.tracesBefore, before)
	return nil
}

func (s *retentionStorage) PurgeAuditLogs(_ context.Context, before time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.auditsBefore = append(s.auditsBefore, before)
	return nil
}

func (s *retentionStorage) purgedTraces() []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Time(nil), s.tracesBefore...)
}

func (s *retentionStorage) purgedAudits() []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Time(nil), s.auditsBefore...)
}

func newRetentionRegistry(t *testing.T, store *retentionStorage) *Registry {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	return &Registry{
		engines:       make(map[string]*activeEngine),
		storage:       store,
		logStorage:    store,
		dashboardSubs: make(map[string]map[chan storage.DashboardStats]bool),
		logger:        telemetry.NewDefaultLogger(),
		startTime:     time.Now(),
		ctx:           ctx,
		cancel:        cancel,
	}
}

func TestPurgeRetention_HonoursDayUnitsTheUIWrites(t *testing.T) {
	for _, unit := range []string{"7d", "30d", "168h"} {
		t.Run(unit, func(t *testing.T) {
			store := &retentionStorage{workflows: []storage.Workflow{
				{ID: "wf-1", TraceRetention: unit, AuditRetention: unit},
			}}
			reg := newRetentionRegistry(t, store)

			reg.purgeRetention()

			traces := store.purgedTraces()
			if len(traces) != 1 {
				t.Fatalf("trace retention %q purged %d times, want 1; message_trace_steps "+
					"grows by the full payload twice per node per message, so a skipped "+
					"sweep is unbounded disk", unit, len(traces))
			}
			want, err := parseDuration(unit)
			if err != nil {
				t.Fatalf("parseDuration(%q): %v", unit, err)
			}
			if got := time.Since(traces[0]); got < want-time.Minute || got > want+time.Minute {
				t.Errorf("purged traces older than %v, want about %v", got, want)
			}
			if len(store.purgedAudits()) != 1 {
				t.Errorf("audit retention %q was skipped too", unit)
			}
		})
	}
}

// "0" and unset both mean "keep everything", and must not be read as
// "delete everything" — parseDuration("") returns a zero duration with no
// error, which would sweep the table clean.
func TestPurgeRetention_UnsetOrZeroKeepsEverything(t *testing.T) {
	for _, unit := range []string{"", "0"} {
		t.Run("value="+unit, func(t *testing.T) {
			store := &retentionStorage{workflows: []storage.Workflow{
				{ID: "wf-1", TraceRetention: unit, AuditRetention: unit},
			}}
			reg := newRetentionRegistry(t, store)

			reg.purgeRetention()

			if got := len(store.purgedTraces()); got != 0 {
				t.Errorf("purged traces %d times for retention %q; unset means keep, "+
					"not delete", got, unit)
			}
		})
	}
}
