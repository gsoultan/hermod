package registry

import (
	"context"
	"testing"
	"time"

	"github.com/user/hermod/internal/storage"
)

func TestNewRegistry_NoPanicWithoutStorage(t *testing.T) {
	// This should not panic even if storage is nil
	// NewRegistry calls startReconciliationLoop which calls reconcileSuspendedMessages
	reg := NewRegistry(nil)
	defer reg.Close()

	// Give it a moment to run the initial reconciliation
	time.Sleep(100 * time.Millisecond)

	// If we are here, it didn't panic
}

func TestReconcileSuspendedMessages_NilStorage(t *testing.T) {
	reg := &Registry{
		storage: nil,
	}

	// Should not panic
	reg.reconcileSuspendedMessages(context.Background())
}

func TestRegistry_Methods_NilStorage(t *testing.T) {
	reg := &Registry{
		storage: nil,
	}
	ctx := context.Background()

	// Test various methods that use storage
	_, err := reg.GetNodeStates(ctx, "wf1")
	if err != nil {
		t.Errorf("GetNodeStates failed: %v", err)
	}

	err = reg.UpdateNodeState(ctx, "wf1", "n1", "state")
	if err != nil {
		t.Errorf("UpdateNodeState failed: %v", err)
	}

	_, err = reg.GetSourceFormSamples(ctx, "path", 10)
	if err != nil {
		t.Errorf("GetSourceFormSamples failed: %v", err)
	}

	stats, err := reg.GetDashboardStats(ctx, "")
	if err != nil {
		t.Errorf("GetDashboardStats failed: %v", err)
	}
	if stats.TotalWorkflows != 0 {
		t.Errorf("Expected 0 total workflows, got %d", stats.TotalWorkflows)
	}
}

func TestRegistry_ValidateWorkflow_NilStorage(t *testing.T) {
	reg := NewRegistry(nil)
	wf := storage.Workflow{
		ID:               "wf1",
		PrioritizeDLQ:    true,
		DeadLetterSinkID: "dlq1",
		Nodes: []storage.WorkflowNode{
			{ID: "n1", Type: "sink", RefID: "s1"},
		},
	}

	// This used to panic or might panic if storage is used without check
	err := reg.ValidateWorkflow(context.Background(), wf)
	if err == nil {
		// It might fail because DLQ sink is not found, but it shouldn't panic
	}
}
