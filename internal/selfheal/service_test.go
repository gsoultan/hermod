package selfheal_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/gsoultan/hermod/internal/optimizer"
	"github.com/gsoultan/hermod/internal/selfheal"
	"github.com/gsoultan/hermod/internal/storage"
)

// store is the settings KV plus the workflows.
type store struct {
	mu        sync.Mutex
	settings  map[string]string
	workflows map[string]storage.Workflow
}

func newStore(wfs ...storage.Workflow) *store {
	s := &store{settings: map[string]string{}, workflows: map[string]storage.Workflow{}}
	for _, wf := range wfs {
		s.workflows[wf.ID] = wf
	}
	return s
}

func (s *store) GetSetting(_ context.Context, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings[key], nil
}

func (s *store) SaveSetting(_ context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings[key] = value
	return nil
}

func (s *store) GetWorkflow(_ context.Context, id string) (storage.Workflow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	wf, ok := s.workflows[id]
	if !ok {
		return storage.Workflow{}, storage.ErrNotFound
	}
	return wf, nil
}

// applier stands in for the workflow update path.
type applier struct {
	store  *store
	before []storage.Workflow
	after  []storage.Workflow
	err    error
}

func (a *applier) ApplyWorkflowChange(_ context.Context, before, after storage.Workflow, _, _ string) (int, int, error) {
	if a.err != nil {
		return 0, 0, a.err
	}
	a.before = append(a.before, before)
	a.after = append(a.after, after)
	a.store.mu.Lock()
	a.store.workflows[after.ID] = after
	a.store.mu.Unlock()
	return 4, 5, nil
}

func retrySuggestion() optimizer.Suggestion {
	return optimizer.Suggestion{WorkflowID: "wf", NodeID: "n1", Action: optimizer.ActionIncreaseRetry, Reason: "n1 fails a lot"}
}

func TestProposeStoresAPendingPatchAndChangesNothing(t *testing.T) {
	st := newStore(storage.Workflow{ID: "wf", Name: "orders", MaxRetries: 2, RetryInterval: "1s"})
	svc := selfheal.NewService(st, nil)

	if err := svc.Propose(t.Context(), retrySuggestion()); err != nil {
		t.Fatal(err)
	}

	if wf, _ := st.GetWorkflow(t.Context(), "wf"); wf.MaxRetries != 2 || wf.RetryInterval != "1s" {
		t.Fatalf("proposing changed the workflow: %+v", wf)
	}
	got, err := svc.List(t.Context(), "wf")
	if err != nil || len(got) != 1 {
		t.Fatalf("list = %+v, %v", got, err)
	}
	p := got[0]
	if p.ID == "" || p.Status != selfheal.StatusPending || p.Kind != string(optimizer.ActionIncreaseRetry) || p.NodeID != "n1" || p.Reason == "" {
		t.Fatalf("proposal = %+v", p)
	}
	patch, _ := json.Marshal(p.Patch)
	want := `[{"op":"replace","path":"/max_retries","before":2,"after":3},{"op":"replace","path":"/retry_interval","before":"1s","after":"2s"}]`
	if string(patch) != want {
		t.Fatalf("patch = %s\nwant    %s", patch, want)
	}
}

func TestProposeUsesTheEngineDefaultsWhenTheWorkflowHasNone(t *testing.T) {
	st := newStore(storage.Workflow{ID: "wf"})
	svc := selfheal.NewService(st, nil)
	if err := svc.Propose(t.Context(), retrySuggestion()); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.List(t.Context(), "wf")
	patch, _ := json.Marshal(got[0].Patch)
	want := `[{"op":"replace","path":"/max_retries","before":0,"after":4},{"op":"replace","path":"/retry_interval","before":"","after":"200ms"}]`
	if string(patch) != want {
		t.Fatalf("patch = %s\nwant    %s", patch, want)
	}
}

func TestProposeIsCappedAndDoesNotRepeatItself(t *testing.T) {
	st := newStore(storage.Workflow{ID: "wf", MaxRetries: 10, RetryInterval: "10s"}, storage.Workflow{ID: "wf2", MaxRetries: 1})
	svc := selfheal.NewService(st, nil)

	if err := svc.Propose(t.Context(), retrySuggestion()); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.List(t.Context(), "wf"); len(got) != 0 {
		t.Fatalf("a proposal past the caps: %+v", got)
	}

	s := retrySuggestion()
	s.WorkflowID = "wf2"
	for range 3 {
		if err := svc.Propose(t.Context(), s); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := svc.List(t.Context(), "wf2")
	if len(got) != 1 || got[0].Occurrences != 3 {
		t.Fatalf("pending proposals = %+v", got)
	}
}

func TestApproveAppliesThroughTheUpdatePath(t *testing.T) {
	st := newStore(storage.Workflow{ID: "wf", Name: "orders", MaxRetries: 2, RetryInterval: "1s", Tags: []string{"x"}})
	ap := &applier{store: st}
	svc := selfheal.NewService(st, ap)
	_ = svc.Propose(t.Context(), retrySuggestion())
	pending, _ := svc.List(t.Context(), "wf")

	p, err := svc.Approve(t.Context(), "wf", pending[0].ID, "ada")
	if err != nil {
		t.Fatal(err)
	}
	if len(ap.after) != 1 {
		t.Fatalf("the update path was called %d times", len(ap.after))
	}
	if b, a := ap.before[0], ap.after[0]; b.MaxRetries != 2 || a.MaxRetries != 3 || a.RetryInterval != "2s" || a.Name != "orders" || len(a.Tags) != 1 {
		t.Fatalf("before %+v after %+v", b, a)
	}
	if p.Status != selfheal.StatusApplied || p.DecidedBy != "ada" || p.DecidedAt == nil || p.PreviousVersion != 4 || p.AppliedVersion != 5 {
		t.Fatalf("proposal = %+v", p)
	}
	if _, err := svc.Approve(t.Context(), "wf", p.ID, "ada"); !errors.Is(err, selfheal.ErrNotPending) {
		t.Fatalf("second approval: %v", err)
	}
}

func TestApproveRefusesAPatchTheWorkflowHasMovedPast(t *testing.T) {
	st := newStore(storage.Workflow{ID: "wf", MaxRetries: 2, RetryInterval: "1s"})
	ap := &applier{store: st}
	svc := selfheal.NewService(st, ap)
	_ = svc.Propose(t.Context(), retrySuggestion())
	pending, _ := svc.List(t.Context(), "wf")

	// Someone edits the workflow before the proposal is approved.
	st.workflows["wf"] = storage.Workflow{ID: "wf", MaxRetries: 7, RetryInterval: "1s"}

	_, err := svc.Approve(t.Context(), "wf", pending[0].ID, "ada")
	if !errors.Is(err, selfheal.ErrStale) {
		t.Fatalf("err = %v", err)
	}
	if len(ap.after) != 0 {
		t.Fatal("a stale patch was applied")
	}
	got, _ := svc.List(t.Context(), "wf")
	if got[0].Status != selfheal.StatusStale {
		t.Fatalf("status = %s", got[0].Status)
	}
}

func TestApproveKeepsTheProposalPendingWhenTheUpdateFails(t *testing.T) {
	st := newStore(storage.Workflow{ID: "wf", MaxRetries: 2})
	ap := &applier{store: st, err: errors.New("invalid workflow: no source")}
	svc := selfheal.NewService(st, ap)
	_ = svc.Propose(t.Context(), retrySuggestion())
	pending, _ := svc.List(t.Context(), "wf")

	if _, err := svc.Approve(t.Context(), "wf", pending[0].ID, "ada"); err == nil || !strings.Contains(err.Error(), "no source") {
		t.Fatalf("err = %v", err)
	}
	got, _ := svc.List(t.Context(), "wf")
	if got[0].Status != selfheal.StatusPending {
		t.Fatalf("status = %s", got[0].Status)
	}
}

func TestRejectChangesNothing(t *testing.T) {
	st := newStore(storage.Workflow{ID: "wf", MaxRetries: 2})
	ap := &applier{store: st}
	svc := selfheal.NewService(st, ap)
	_ = svc.Propose(t.Context(), retrySuggestion())
	pending, _ := svc.List(t.Context(), "wf")

	p, err := svc.Reject(t.Context(), "wf", pending[0].ID, "ada")
	if err != nil || p.Status != selfheal.StatusRejected || p.DecidedBy != "ada" {
		t.Fatalf("reject = %+v, %v", p, err)
	}
	if len(ap.after) != 0 {
		t.Fatal("rejecting applied the patch")
	}
	if _, err := svc.Reject(t.Context(), "wf", "nope", "ada"); !errors.Is(err, selfheal.ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
	if _, err := svc.Approve(t.Context(), "wf", p.ID, "ada"); !errors.Is(err, selfheal.ErrNotPending) {
		t.Fatalf("approving a rejected proposal: %v", err)
	}
}
