package aibudget

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	_ "modernc.org/sqlite"

	"github.com/gsoultan/hermod/internal/storage"
	sqlstore "github.com/gsoultan/hermod/internal/storage/sql"
	"github.com/gsoultan/hermod/pkg/engine/telemetry"
	"github.com/gsoultan/hermod/pkg/llm"
)

// newStore is a real SQL store on an in-memory SQLite database.
func newStore(t *testing.T) storage.Storage {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("file:aibudget_%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st := sqlstore.NewSQLStorage(db, "sqlite")
	if err := st.Init(t.Context()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return st
}

// notice is one alert raised through the notifier.
type notice struct{ level, title, message string }

type fakeNotifier struct {
	mu      sync.Mutex
	notices []notice
}

func (n *fakeNotifier) NotifyLevel(_ context.Context, level, title, message string, _ storage.Workflow) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.notices = append(n.notices, notice{level, title, message})
}

func (n *fakeNotifier) count(level string) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	c := 0
	for _, x := range n.notices {
		if x.level == level {
			c++
		}
	}
	return c
}

var october = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

func newService(t *testing.T, st storage.Storage, n Notifier) *Service {
	t.Helper()
	s := NewService(func() any { return st }, n, nil)
	s.now = func() time.Time { return october }
	return s
}

func put(t *testing.T, st storage.Storage, b storage.AIBudget) {
	t.Helper()
	if err := st.(storage.AIBudgetStore).PutAIBudget(t.Context(), b); err != nil {
		t.Fatalf("PutAIBudget: %v", err)
	}
}

func scoped(t *testing.T, vhost, workflowID string) context.Context {
	return llm.WithScope(t.Context(), llm.Scope{VHost: vhost, WorkflowID: workflowID})
}

func spend(ctx context.Context, t *testing.T, s *Service, model string, in, out int64) {
	t.Helper()
	s.Record(ctx, llm.CallRecord{Provider: "openai", Model: model, Usage: llm.Usage{InputTokens: in, OutputTokens: out}})
}

func wantLimit(t *testing.T, err error, limit llm.Limit) {
	t.Helper()
	var be *llm.BudgetError
	if !errors.As(err, &be) || be.Limit != limit {
		t.Fatalf("err = %v, want a BudgetError for %s", err, limit)
	}
	if !errors.Is(err, llm.ErrBudgetExceeded) {
		t.Fatalf("err = %v does not match llm.ErrBudgetExceeded", err)
	}
}

func TestNoBudgetAllowsAndStillCountsUsage(t *testing.T) {
	st := newStore(t)
	s := newService(t, st, nil)
	ctx := scoped(t, "tenant-a", "wf-1")
	if err := s.Allow(ctx); err != nil {
		t.Fatalf("Allow with no budget: %v", err)
	}
	spend(ctx, t, s, "m", 10, 5)

	r, err := s.Report(t.Context(), "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	if r.Period != "2026-10" || r.Usage.Tokens() != 15 || r.Usage.Calls != 1 {
		t.Fatalf("vhost usage = %+v in %s", r.Usage, r.Period)
	}
	if len(r.Workflows) != 1 || r.Workflows[0].WorkflowID != "wf-1" || r.Workflows[0].Tokens() != 15 {
		t.Fatalf("workflow usage = %+v", r.Workflows)
	}
}

func TestKillSwitchRefusesEveryCall(t *testing.T) {
	st := newStore(t)
	put(t, st, storage.AIBudget{VHost: "tenant-a", Disabled: true})
	s := newService(t, st, nil)
	blocked := telemetry.AIBudgetBlocked.WithLabelValues("tenant-a", string(llm.LimitDisabled))
	before := testutil.ToFloat64(blocked)

	wantLimit(t, s.Allow(scoped(t, "tenant-a", "")), llm.LimitDisabled)
	if d := testutil.ToFloat64(blocked) - before; d != 1 {
		t.Fatalf("blocked counter moved by %v, want 1", d)
	}
	if err := s.Allow(scoped(t, "tenant-b", "")); err != nil {
		t.Fatalf("another vhost was switched off: %v", err)
	}
}

// The check is made before the call and the usage counted after it, so the
// call that crosses the limit completes and the next one is refused.
func TestVHostTokenBudgetRefusesOnceSpent(t *testing.T) {
	st := newStore(t)
	put(t, st, storage.AIBudget{VHost: "v", MonthlyTokens: 100})
	s := newService(t, st, nil)
	ctx := scoped(t, "v", "wf")

	if err := s.Allow(ctx); err != nil {
		t.Fatal(err)
	}
	spend(ctx, t, s, "m", 60, 30)
	if err := s.Allow(ctx); err != nil {
		t.Fatalf("90 of 100 tokens: %v", err)
	}
	spend(ctx, t, s, "m", 15, 0) // 105: overshoots by the last call
	wantLimit(t, s.Allow(ctx), llm.LimitVHostTokens)
	wantLimit(t, s.Allow(scoped(t, "v", "another-workflow")), llm.LimitVHostTokens)
}

func TestWorkflowCapRefusesOnlyThatWorkflow(t *testing.T) {
	st := newStore(t)
	put(t, st, storage.AIBudget{VHost: "v", Workflows: []storage.AIWorkflowCap{{WorkflowID: "wf-1", MonthlyTokens: 10}}})
	s := newService(t, st, nil)

	spend(scoped(t, "v", "wf-1"), t, s, "m", 8, 4)
	err := s.Allow(scoped(t, "v", "wf-1"))
	wantLimit(t, err, llm.LimitWorkflowTokens)
	if !strings.Contains(err.Error(), "wf-1") {
		t.Fatalf("message %q does not name the workflow", err)
	}
	if err := s.Allow(scoped(t, "v", "wf-2")); err != nil {
		t.Fatalf("an uncapped workflow was refused: %v", err)
	}
}

func TestCostBudgetPricesEachModel(t *testing.T) {
	st := newStore(t)
	put(t, st, storage.AIBudget{VHost: "v", MonthlyCost: 1, Prices: []storage.AIModelPrice{
		{Model: "big", InputPerMillion: 10_000, OutputPerMillion: 0},
		{Model: storage.AnyModel, InputPerMillion: 1, OutputPerMillion: 1},
	}})
	s := newService(t, st, nil)
	ctx := scoped(t, "v", "")

	spend(ctx, t, s, "small", 1000, 1000) // 0.002
	if err := s.Allow(ctx); err != nil {
		t.Fatalf("0.002 of 1.00 spent: %v", err)
	}
	spend(ctx, t, s, "big", 100, 0) // 1.00
	wantLimit(t, s.Allow(ctx), llm.LimitVHostCost)

	r, _ := s.Report(t.Context(), "v")
	if r.Usage.CostMicros != 1_002_000 {
		t.Fatalf("cost = %d micros, want 1002000", r.Usage.CostMicros)
	}
}

// A new calendar month starts from zero.
func TestUsageIsPerUTCMonth(t *testing.T) {
	st := newStore(t)
	put(t, st, storage.AIBudget{VHost: "v", MonthlyTokens: 10})
	s := newService(t, st, nil)
	ctx := scoped(t, "v", "")
	spend(ctx, t, s, "m", 20, 0)
	wantLimit(t, s.Allow(ctx), llm.LimitVHostTokens)

	s.now = func() time.Time { return time.Date(2026, 11, 1, 0, 0, 1, 0, time.UTC) }
	if err := s.Allow(ctx); err != nil {
		t.Fatalf("November was refused for October's spend: %v", err)
	}
}

// The alert at 80% goes out once per scope and month: not on every call after
// it, and not once per replica.
func TestEightyPercentAlertIsRaisedOnce(t *testing.T) {
	st := newStore(t)
	put(t, st, storage.AIBudget{VHost: "alerting", MonthlyTokens: 100,
		Workflows: []storage.AIWorkflowCap{{WorkflowID: "wf-1", MonthlyTokens: 50}}})
	n := &fakeNotifier{}
	replicaA, replicaB := newService(t, st, n), newService(t, st, n)
	warned := telemetry.AIBudgetWarnings.WithLabelValues("alerting", "vhost", storage.AIWarnTokens)
	before := testutil.ToFloat64(warned)
	ctx := scoped(t, "alerting", "wf-1")

	spend(ctx, t, replicaA, "m", 30, 0) // vhost 30%, wf-1 60%
	if got := n.count(LevelWarn); got != 0 {
		t.Fatalf("%d alerts below 80%%", got)
	}
	spend(ctx, t, replicaA, "m", 15, 0) // vhost 45%, wf-1 90%
	if got := n.count(LevelWarn); got != 1 {
		t.Fatalf("alerts = %d, want 1 (the workflow's)", got)
	}
	spend(ctx, t, replicaB, "m", 40, 0) // vhost 85%
	spend(ctx, t, replicaA, "m", 1, 0)
	spend(ctx, t, replicaB, "m", 1, 0)
	if got := n.count(LevelWarn); got != 2 {
		t.Fatalf("alerts = %d, want 2: one for the workflow, one for the vhost", got)
	}
	if d := testutil.ToFloat64(warned) - before; d != 1 {
		t.Fatalf("vhost warnings counter moved by %v, want 1", d)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if !strings.Contains(n.notices[0].title, "wf-1") || !strings.Contains(n.notices[1].title, "alerting") {
		t.Fatalf("alerts do not name their scope: %+v", n.notices)
	}
}

// A spent budget is alerted once, not once per refused message; the kill
// switch, an operator's own choice, is not alerted at all.
func TestRefusalsAreAlertedOncePerInterval(t *testing.T) {
	st := newStore(t)
	put(t, st, storage.AIBudget{VHost: "v", MonthlyTokens: 1})
	put(t, st, storage.AIBudget{VHost: "off", Disabled: true})
	n := &fakeNotifier{}
	s := newService(t, st, n)
	spend(scoped(t, "v", ""), t, s, "m", 5, 0)
	for range 3 {
		wantLimit(t, s.Allow(scoped(t, "v", "")), llm.LimitVHostTokens)
		wantLimit(t, s.Allow(scoped(t, "off", "")), llm.LimitDisabled)
	}
	if got := n.count(LevelError); got != 1 {
		t.Fatalf("refusal alerts = %d, want 1", got)
	}
}

// Budgets fail closed: a budget that cannot be read refuses the call.
func TestUnreadableBudgetFailsClosed(t *testing.T) {
	s := NewService(func() any { return brokenStore{} }, nil, nil)
	wantLimit(t, s.Allow(scoped(t, "v", "")), llm.LimitUnavailable)
}

// A backend that cannot hold budgets has none to enforce.
func TestStoreWithoutBudgetsAllows(t *testing.T) {
	s := NewService(func() any { return struct{}{} }, nil, nil)
	if err := s.Allow(scoped(t, "v", "")); err != nil {
		t.Fatalf("Allow = %v", err)
	}
	s.Record(scoped(t, "v", ""), llm.CallRecord{Usage: llm.Usage{InputTokens: 1}})
	if _, err := s.Report(t.Context(), "v"); !errors.Is(err, storage.ErrAIBudgetsUnsupported) {
		t.Fatalf("Report = %v, want ErrAIBudgetsUnsupported", err)
	}
}

// A call with no vhost is the default vhost's, as a workflow with no vhost is.
func TestEmptyVHostIsTheDefaultVHost(t *testing.T) {
	st := newStore(t)
	put(t, st, storage.AIBudget{VHost: "default", Disabled: true})
	s := newService(t, st, nil)
	wantLimit(t, s.Allow(t.Context()), llm.LimitDisabled)
}

// A saved budget applies to this replica at once; other replicas see it
// when their cached copy expires.
func TestInvalidateAppliesASavedBudgetAtOnce(t *testing.T) {
	st := newStore(t)
	s := newService(t, st, nil)
	ctx := scoped(t, "v", "")
	if err := s.Allow(ctx); err != nil {
		t.Fatal(err)
	}
	put(t, st, storage.AIBudget{VHost: "v", Disabled: true})
	s.Invalidate("v")
	wantLimit(t, s.Allow(ctx), llm.LimitDisabled)
}

// remoteStore is a worker's storage: it answers through the control plane.
type remoteStore struct {
	mu      sync.Mutex
	checked []llm.Scope
	usage   []llm.CallRecord
	refuse  error
}

func (r *remoteStore) CheckAIBudget(_ context.Context, vhost, workflowID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checked = append(r.checked, llm.Scope{VHost: vhost, WorkflowID: workflowID})
	return r.refuse
}

func (r *remoteStore) RecordAIUsage(_ context.Context, _, _ string, rec llm.CallRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.usage = append(r.usage, rec)
	return nil
}

func TestWorkerDelegatesToTheControlPlane(t *testing.T) {
	r := &remoteStore{refuse: &llm.BudgetError{Limit: llm.LimitDisabled, VHost: "v"}}
	s := NewService(func() any { return r }, nil, nil)
	ctx := scoped(t, "v", "wf")
	wantLimit(t, s.Allow(ctx), llm.LimitDisabled)
	spend(ctx, t, s, "m", 3, 4)
	if len(r.checked) != 1 || r.checked[0] != (llm.Scope{VHost: "v", WorkflowID: "wf"}) {
		t.Fatalf("checked = %+v", r.checked)
	}
	if len(r.usage) != 1 || r.usage[0].Usage.OutputTokens != 4 || r.usage[0].Model != "m" {
		t.Fatalf("usage = %+v", r.usage)
	}
}

// brokenStore holds budgets but cannot be read.
type brokenStore struct{ storage.AIBudgetStore }

func (brokenStore) GetAIBudget(context.Context, string) (storage.AIBudget, error) {
	return storage.AIBudget{}, errors.New("connection refused")
}
