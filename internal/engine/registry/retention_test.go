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
	total        int // 0 means "exactly what workflows holds"
	policies     []storage.TraceRetention
	auditsBefore []time.Time
}

func (s *retentionStorage) ListWorkflows(context.Context, storage.CommonFilter) ([]storage.Workflow, int, error) {
	if s.total > 0 {
		return s.workflows, s.total, nil
	}
	return s.workflows, len(s.workflows), nil
}

// One call per sweep now, carrying every workflow's own window. It used to be
// one call per workflow carrying a bare cutoff, against a DELETE with no
// workflow predicate — so the shortest window in the deployment decided what
// every workflow kept.
func (s *retentionStorage) PurgeMessageTraces(_ context.Context, retention storage.TraceRetention) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.policies = append(s.policies, retention)
	return nil
}

func (s *retentionStorage) PurgeAuditLogs(_ context.Context, before time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.auditsBefore = append(s.auditsBefore, before)
	return nil
}

func (s *retentionStorage) tracePolicies() []storage.TraceRetention {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]storage.TraceRetention(nil), s.policies...)
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

			policies := store.tracePolicies()
			if len(policies) != 1 {
				t.Fatalf("trace retention %q swept %d times, want exactly 1; a skipped "+
					"sweep is unbounded disk on the largest table Hermod owns, and a "+
					"repeated one is a full-table delete per workflow per hour",
					unit, len(policies))
			}
			cutoff, ok := policies[0].Keep["wf-1"]
			if !ok {
				t.Fatalf("trace retention %q produced no cutoff for its own workflow", unit)
			}
			want, err := parseDuration(unit)
			if err != nil {
				t.Fatalf("parseDuration(%q): %v", unit, err)
			}
			if got := time.Since(cutoff); got < want-time.Minute || got > want+time.Minute {
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

			policies := store.tracePolicies()
			if len(policies) != 1 {
				t.Fatalf("the sweep must still run for %q — it is what reclaims the "+
					"traces of deleted workflows — but it ran %d times", unit, len(policies))
			}
			if _, ok := policies[0].Keep["wf-1"]; ok {
				t.Errorf("a cutoff was produced for retention %q; unset means keep, "+
					"not delete", unit)
			}
		})
	}
}

// The assembly: what purgeRetention hands the storage layer.
//
// Every workflow's own window in one policy, one sweep, and — the part that
// decides whether traces of deleted workflows may be reclaimed at all — an
// honest answer about whether the workflow list was complete.
func TestPurgeRetention_BuildsOnePolicyForEveryWorkflow(t *testing.T) {
	store := &retentionStorage{workflows: []storage.Workflow{
		{ID: "wf-short", TraceRetention: "7d"},
		{ID: "wf-long", TraceRetention: "365d"},
		{ID: "wf-unset"},
	}}
	reg := newRetentionRegistry(t, store)

	reg.purgeRetention()

	policies := store.tracePolicies()
	if len(policies) != 1 {
		t.Fatalf("three workflows produced %d sweeps, want 1", len(policies))
	}
	p := policies[0]

	short, ok := p.Keep["wf-short"]
	if !ok {
		t.Fatal("wf-short has no cutoff")
	}
	long, ok := p.Keep["wf-long"]
	if !ok {
		t.Fatal("wf-long has no cutoff")
	}
	if !short.After(long) {
		t.Errorf("the 7d cutoff (%v) is not more recent than the 365d one (%v); "+
			"each workflow must carry its own window, or the shortest wins again", short, long)
	}
	if _, ok := p.Keep["wf-unset"]; ok {
		t.Error("a workflow with no window got a cutoff; unset means keep everything")
	}

	// Every workflow is live, including the one with no window — that is what
	// separates "keeps everything" from "deleted".
	for _, id := range []string{"wf-short", "wf-long", "wf-unset"} {
		if _, ok := p.Live[id]; !ok {
			t.Errorf("%s is missing from Live and would be swept as a deleted workflow", id)
		}
	}
	if !p.LiveIsComplete {
		t.Error("the list was complete and the policy says otherwise, so orphans are never reclaimed")
	}
}

// A deployment with more workflows than one page must not have everything past
// the first page mistaken for deleted workflows.
func TestPurgeRetention_RefusesToClaimACompleteListItDoesNotHave(t *testing.T) {
	store := &retentionStorage{
		workflows: []storage.Workflow{{ID: "wf-1", TraceRetention: "7d"}},
		total:     workflowPageForRetention + 1,
	}
	reg := newRetentionRegistry(t, store)

	reg.purgeRetention()

	policies := store.tracePolicies()
	if len(policies) != 1 {
		t.Fatalf("got %d sweeps, want 1", len(policies))
	}
	if policies[0].LiveIsComplete {
		t.Error("the sweep claimed a complete workflow list while paging cut it off; " +
			"every workflow past the first page would have its traces deleted as an orphan")
	}
	// The per-workflow windows still apply — only the orphan sweep is withheld.
	if _, ok := policies[0].Keep["wf-1"]; !ok {
		t.Error("an incomplete list also stopped the workflows it could see from being trimmed")
	}
}
