package engine

import (
	"context"
	"testing"

	"github.com/user/hermod/internal/storage"
)

type mockFormStorage struct {
	mockStorage
	submissions []storage.FormSubmission
}

func (m *mockFormStorage) ListFormSubmissions(ctx context.Context, filter storage.FormSubmissionFilter) ([]storage.FormSubmission, int, error) {
	var result []storage.FormSubmission
	for _, s := range m.submissions {
		if s.Path == filter.Path {
			result = append(result, s)
			if len(result) >= filter.Limit {
				break
			}
		}
	}
	return result, len(result), nil
}

func (m *mockFormStorage) ListWorkflows(ctx context.Context, filter storage.CommonFilter) ([]storage.Workflow, int, error) {
	return nil, 0, nil
}

func TestFormSampling(t *testing.T) {
	storage := &mockFormStorage{
		submissions: []storage.FormSubmission{
			{
				ID:   "sub1",
				Path: "/api/forms/test",
				Data: []byte(`{"name": "test-user", "message": "hello"}`),
			},
		},
	}
	registry := NewRegistry(storage)
	ctx := t.Context()

	cfg := SourceConfig{
		Type: "form",
		Config: map[string]string{
			"path": "/api/forms/test",
		},
	}

	msg, err := registry.SampleTable(ctx, cfg, "form")
	if err != nil {
		t.Fatalf("SampleTable failed: %v", err)
	}

	if msg == nil {
		t.Fatal("Expected message, got nil")
	}

	if msg.ID() != "sub1" {
		t.Errorf("Expected message ID sub1, got %s", msg.ID())
	}

	if string(msg.After()) != `{"name": "test-user", "message": "hello"}` {
		t.Errorf("Unexpected message data: %s", string(msg.After()))
	}

	// Test path not found
	cfg.Config["path"] = "/api/forms/missing"
	_, err = registry.SampleTable(ctx, cfg, "form")
	if err == nil {
		t.Error("Expected error for missing path, got nil")
	}
}
