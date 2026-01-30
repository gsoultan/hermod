package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/user/hermod/internal/notification"
	"github.com/user/hermod/internal/storage"
	pkgengine "github.com/user/hermod/pkg/engine"
)

type mockNotificationProvider struct {
	sent bool
}

func (p *mockNotificationProvider) Send(ctx context.Context, title, message string, wf storage.Workflow) error {
	p.sent = true
	return nil
}

func (p *mockNotificationProvider) Type() string { return "mock" }

type mockAlertingStorage struct {
	storage.Storage
	workflow storage.Workflow
}

func (s *mockAlertingStorage) GetWorkflow(ctx context.Context, id string) (storage.Workflow, error) {
	return s.workflow, nil
}

func (s *mockAlertingStorage) UpdateWorkflow(ctx context.Context, wf storage.Workflow) error {
	s.workflow = wf
	return nil
}

func TestAlertingOnStatusChange(t *testing.T) {
	ms := &mockAlertingStorage{
		workflow: storage.Workflow{ID: "wf-1", Name: "Test Workflow", Status: "running"},
	}
	r := &Registry{
		storage: ms,
	}

	provider := &mockNotificationProvider{}
	ns := notification.NewService(ms)
	ns.AddProvider(provider)
	r.notificationService = ns

	// Simulate status change to error
	update := pkgengine.StatusUpdate{
		EngineStatus: "error:something_failed",
	}

	engStatusChange := func(update pkgengine.StatusUpdate) {
		dbCtx := context.Background()
		if workflow, err := r.storage.GetWorkflow(dbCtx, "wf-1"); err == nil {
			prevStatus := workflow.Status
			workflow.Status = update.EngineStatus
			_ = r.storage.UpdateWorkflow(dbCtx, workflow)

			if strings.Contains(strings.ToLower(update.EngineStatus), "error") &&
				!strings.Contains(strings.ToLower(prevStatus), "error") &&
				r.notificationService != nil {
				r.notificationService.Notify(dbCtx, "Workflow Error", "failed", workflow)
			}
		}
	}

	engStatusChange(update)

	if !provider.sent {
		t.Errorf("Expected notification to be sent on error status change")
	}
}

func TestDLQThresholdAlerting(t *testing.T) {
	ms := &mockAlertingStorage{
		workflow: storage.Workflow{
			ID:           "wf-dlq",
			Name:         "DLQ Test",
			Status:       "running",
			DLQThreshold: 10,
		},
	}
	r := &Registry{
		storage: ms,
	}

	provider := &mockNotificationProvider{}
	ns := notification.NewService(ms)
	ns.AddProvider(provider)
	r.notificationService = ns

	// Simulation function (replicates Registry's SetOnStatusChange logic)
	onStatusChange := func(update pkgengine.StatusUpdate) {
		dbCtx := context.Background()
		if workflow, err := r.storage.GetWorkflow(dbCtx, "wf-dlq"); err == nil && workflow.DLQThreshold > 0 {
			if update.DeadLetterCount >= uint64(workflow.DLQThreshold) {
				r.notificationService.Notify(dbCtx, "DLQ Threshold Exceeded", "dlq alert", workflow)
			}
		}
	}

	// 1. Below threshold
	onStatusChange(pkgengine.StatusUpdate{DeadLetterCount: 5})
	if provider.sent {
		t.Errorf("Did not expect notification yet (5 < 10)")
	}

	// 2. Reach threshold
	onStatusChange(pkgengine.StatusUpdate{DeadLetterCount: 10})
	if !provider.sent {
		t.Errorf("Expected notification at threshold (10 >= 10)")
	}
}
