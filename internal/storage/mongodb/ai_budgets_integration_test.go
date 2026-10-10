//go:build integration
// +build integration

package mongodb

import (
	"errors"
	"sync"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

// The same contract the SQL store keeps (internal/storage/sql/ai_budgets_test.go),
// against a live MongoDB.
func TestMongoAIBudgets(t *testing.T) {
	s, _ := newTraceMongo(t)
	st, ok := s.(storage.AIBudgetStore)
	if !ok {
		t.Fatal("the MongoDB store does not implement storage.AIBudgetStore")
	}
	ctx := t.Context()
	if _, err := st.GetAIBudget(ctx, "tenant-a"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("no budget yet: err = %v", err)
	}
	b := storage.AIBudget{
		VHost: "tenant-a", Disabled: true, MonthlyTokens: 100, MonthlyCost: 5,
		Prices:    []storage.AIModelPrice{{Model: storage.AnyModel, InputPerMillion: 1, OutputPerMillion: 2}},
		Workflows: []storage.AIWorkflowCap{{WorkflowID: "wf-1", MonthlyTokens: 10}},
		UpdatedBy: "ada",
	}
	if err := st.PutAIBudget(ctx, b); err != nil {
		t.Fatalf("PutAIBudget: %v", err)
	}
	got, err := st.GetAIBudget(ctx, "tenant-a")
	if err != nil {
		t.Fatalf("GetAIBudget: %v", err)
	}
	if !got.Disabled || got.MonthlyCost != 5 || len(got.Prices) != 1 || len(got.Workflows) != 1 || got.UpdatedBy != "ada" {
		t.Fatalf("round trip lost fields: %+v", got)
	}

	const n = 10
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			if _, err := st.AddAIUsage(ctx, "tenant-a", "2026-10", "wf-1", storage.AIUsageDelta{InputTokens: 2, OutputTokens: 1, CostMicros: 3}); err != nil {
				t.Errorf("AddAIUsage: %v", err)
			}
		})
	}
	wg.Wait()
	u, err := st.GetAIUsage(ctx, "tenant-a", "2026-10", "wf-1")
	if err != nil || u.Calls != n || u.Tokens() != 3*n || u.CostMicros != 3*n {
		t.Fatalf("usage = %+v, %v", u, err)
	}
	if _, err := st.AddAIUsage(ctx, "tenant-a", "2026-10", "", storage.AIUsageDelta{InputTokens: 1}); err != nil {
		t.Fatal(err)
	}
	list, err := st.ListAIUsage(ctx, "tenant-a", "2026-10")
	if err != nil || len(list) != 2 || list[0].WorkflowID != "" || list[1].WorkflowID != "wf-1" {
		t.Fatalf("list = %+v, %v", list, err)
	}

	if first, err := st.MarkAIUsageWarned(ctx, "tenant-a", "2026-10", "wf-1", storage.AIWarnTokens); err != nil || !first {
		t.Fatalf("first mark = %v, %v", first, err)
	}
	if again, _ := st.MarkAIUsageWarned(ctx, "tenant-a", "2026-10", "wf-1", storage.AIWarnTokens); again {
		t.Fatal("the alert was marked twice")
	}

	if err := st.DeleteAIBudgets(ctx, "tenant-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetAIBudget(ctx, "tenant-a"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("budget survived DeleteAIBudgets: %v", err)
	}
	if list, _ := st.ListAIUsage(ctx, "tenant-a", "2026-10"); len(list) != 0 {
		t.Fatalf("usage survived DeleteAIBudgets: %+v", list)
	}
}
