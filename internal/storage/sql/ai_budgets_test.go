package sql

import (
	"errors"
	"sync"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

func aiBudgetStore(t *testing.T) storage.AIBudgetStore {
	t.Helper()
	var st storage.Storage = newRotationStorage(t)
	bs, ok := st.(storage.AIBudgetStore)
	if !ok {
		t.Fatal("the SQL store does not implement AIBudgetStore")
	}
	return bs
}

func sampleBudget(vhost string) storage.AIBudget {
	return storage.AIBudget{
		VHost: vhost, Disabled: true, MonthlyTokens: 1_000_000, MonthlyCost: 50, Currency: "USD",
		Prices:    []storage.AIModelPrice{{Model: storage.AnyModel, InputPerMillion: 3, OutputPerMillion: 15}},
		Workflows: []storage.AIWorkflowCap{{WorkflowID: "wf-1", MonthlyTokens: 1000, MonthlyCost: 5}},
		UpdatedBy: "ada",
	}
}

func TestAIBudgetRoundTripsAndStaysInItsVHost(t *testing.T) {
	st := aiBudgetStore(t)
	ctx := t.Context()
	if _, err := st.GetAIBudget(ctx, "tenant-a"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("a vhost with no budget: err = %v, want ErrNotFound", err)
	}
	if err := st.PutAIBudget(ctx, sampleBudget("tenant-a")); err != nil {
		t.Fatalf("PutAIBudget: %v", err)
	}
	got, err := st.GetAIBudget(ctx, "tenant-a")
	if err != nil {
		t.Fatalf("GetAIBudget: %v", err)
	}
	if !got.Disabled || got.MonthlyTokens != 1_000_000 || got.MonthlyCost != 50 || got.Currency != "USD" ||
		len(got.Prices) != 1 || got.Prices[0].OutputPerMillion != 15 ||
		len(got.Workflows) != 1 || got.Workflows[0].MonthlyCost != 5 || got.UpdatedBy != "ada" || got.UpdatedAt.IsZero() {
		t.Fatalf("round trip lost fields: %+v", got)
	}

	b := sampleBudget("tenant-a")
	b.Disabled = false
	if err := st.PutAIBudget(ctx, b); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetAIBudget(ctx, "tenant-a"); got.Disabled {
		t.Fatal("the second save did not replace the first")
	}
	if _, err := st.GetAIBudget(ctx, "tenant-b"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("another vhost read tenant-a's budget: err = %v", err)
	}
}

func TestPutAIBudgetRefusesAnInvalidBudget(t *testing.T) {
	st := aiBudgetStore(t)
	b := sampleBudget("v")
	b.Prices = nil
	if err := st.PutAIBudget(t.Context(), b); err == nil {
		t.Fatal("a cost limit without prices was saved")
	}
}

func TestAddAIUsageAccumulatesPerScopeAndMonth(t *testing.T) {
	st := aiBudgetStore(t)
	ctx := t.Context()
	add := func(vhost, period, wf string, in, out, cost int64) storage.AIUsage {
		t.Helper()
		u, err := st.AddAIUsage(ctx, vhost, period, wf, storage.AIUsageDelta{InputTokens: in, OutputTokens: out, CostMicros: cost})
		if err != nil {
			t.Fatalf("AddAIUsage: %v", err)
		}
		return u
	}
	add("v", "2026-10", "", 10, 5, 100)
	got := add("v", "2026-10", "", 1, 2, 3)
	if got.Calls != 2 || got.InputTokens != 11 || got.OutputTokens != 7 || got.CostMicros != 103 || got.Tokens() != 18 {
		t.Fatalf("totals after two calls = %+v", got)
	}
	add("v", "2026-10", "wf-1", 4, 4, 4)
	add("v", "2026-11", "", 9, 9, 9)
	add("w", "2026-10", "", 7, 7, 7)

	if u, _ := st.GetAIUsage(ctx, "v", "2026-10", "wf-1"); u.Tokens() != 8 || u.Calls != 1 {
		t.Errorf("wf-1 = %+v", u)
	}
	if u, _ := st.GetAIUsage(ctx, "v", "2026-09", ""); u.Calls != 0 || u.Tokens() != 0 {
		t.Errorf("a month with no usage = %+v, want zero", u)
	}

	list, err := st.ListAIUsage(ctx, "v", "2026-10")
	if err != nil {
		t.Fatalf("ListAIUsage: %v", err)
	}
	if len(list) != 2 || list[0].WorkflowID != "" || list[1].WorkflowID != "wf-1" || list[0].Tokens() != 18 {
		t.Fatalf("list = %+v, want the vhost row then wf-1, nothing of other months or vhosts", list)
	}
}

// Replicas add to the same row at once; every addition must land.
func TestAddAIUsageIsAtomicUnderConcurrency(t *testing.T) {
	st := aiBudgetStore(t)
	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for range n {
		wg.Go(func() {
			if _, err := st.AddAIUsage(t.Context(), "v", "2026-10", "", storage.AIUsageDelta{InputTokens: 1, CostMicros: 2}); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("AddAIUsage: %v", err)
	}
	u, _ := st.GetAIUsage(t.Context(), "v", "2026-10", "")
	if u.Calls != n || u.InputTokens != n || u.CostMicros != 2*n {
		t.Fatalf("usage = %+v, want %d calls", u, n)
	}
}

func TestMarkAIUsageWarnedAnswersTrueOnce(t *testing.T) {
	st := aiBudgetStore(t)
	ctx := t.Context()
	if _, err := st.AddAIUsage(ctx, "v", "2026-10", "", storage.AIUsageDelta{InputTokens: 1}); err != nil {
		t.Fatal(err)
	}
	first, err := st.MarkAIUsageWarned(ctx, "v", "2026-10", "", storage.AIWarnTokens)
	if err != nil || !first {
		t.Fatalf("first mark = %v, %v; want true", first, err)
	}
	if again, _ := st.MarkAIUsageWarned(ctx, "v", "2026-10", "", storage.AIWarnTokens); again {
		t.Fatal("the tokens alert was marked twice")
	}
	if cost, _ := st.MarkAIUsageWarned(ctx, "v", "2026-10", "", storage.AIWarnCost); !cost {
		t.Fatal("the cost alert is separate from the tokens alert")
	}
	u, _ := st.GetAIUsage(ctx, "v", "2026-10", "")
	if !u.TokensWarned || !u.CostWarned {
		t.Fatalf("usage = %+v, want both alerts recorded", u)
	}
	if _, err := st.MarkAIUsageWarned(ctx, "v", "2026-10", "", "bogus"); err == nil {
		t.Fatal("an unknown alert kind was accepted")
	}
}

// A deleted vhost must not leave its budget or usage for a later vhost of the
// same name.
func TestDeletingAVHostDeletesItsAIBudgetAndUsage(t *testing.T) {
	s := newRotationStorage(t)
	ctx := t.Context()
	if err := s.CreateVHost(ctx, storage.VHost{ID: "vh-1", Name: "tenant-a"}); err != nil {
		t.Fatalf("CreateVHost: %v", err)
	}
	for _, vhost := range []string{"tenant-a", "tenant-b"} {
		if err := s.PutAIBudget(ctx, sampleBudget(vhost)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AddAIUsage(ctx, vhost, "2026-10", "", storage.AIUsageDelta{InputTokens: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteVHost(ctx, "vh-1"); err != nil {
		t.Fatalf("DeleteVHost: %v", err)
	}
	if _, err := s.GetAIBudget(ctx, "tenant-a"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("tenant-a was deleted and still has a budget: err = %v", err)
	}
	if list, _ := s.ListAIUsage(ctx, "tenant-a", "2026-10"); len(list) != 0 {
		t.Errorf("tenant-a was deleted and still has usage: %+v", list)
	}
	if _, err := s.GetAIBudget(ctx, "tenant-b"); err != nil {
		t.Errorf("deleting tenant-a removed tenant-b's budget: %v", err)
	}
}
