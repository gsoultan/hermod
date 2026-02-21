package engine

import (
	"context"
	"testing"

	"github.com/user/hermod/internal/storage"
)

type mockRegStorage struct {
	storage.Storage
	workers map[string]storage.Worker
}

func (m *mockRegStorage) GetWorker(ctx context.Context, id string) (storage.Worker, error) {
	w, ok := m.workers[id]
	if !ok {
		return storage.Worker{}, storage.ErrNotFound
	}
	return w, nil
}

func (m *mockRegStorage) CreateWorker(ctx context.Context, w storage.Worker) error {
	m.workers[w.ID] = w
	return nil
}

func TestWorker_SelfRegister(t *testing.T) {
	store := &mockRegStorage{workers: make(map[string]storage.Worker)}
	registry := NewRegistry(store)
	worker := NewWorker(store, registry)

	worker.SetWorkerConfig(0, 1, "test-worker-1", "test-token")
	worker.SetRegistrationInfo("Test Worker", "localhost", 8081, "A test worker")

	ctx := t.Context()
	err := worker.SelfRegister(ctx)
	if err != nil {
		t.Fatalf("SelfRegister failed: %v", err)
	}

	w, err := store.GetWorker(ctx, "test-worker-1")
	if err != nil {
		t.Fatalf("Failed to get worker after registration: %v", err)
	}

	if w.Name != "Test Worker" {
		t.Errorf("Expected name 'Test Worker', got '%s'", w.Name)
	}
	if w.Host != "localhost" {
		t.Errorf("Expected host 'localhost', got '%s'", w.Host)
	}
	if w.Port != 8081 {
		t.Errorf("Expected port 8081, got %d", w.Port)
	}
	if w.Description != "A test worker" {
		t.Errorf("Expected description 'A test worker', got '%s'", w.Description)
	}
	if w.Token != "test-token" {
		t.Errorf("Expected token 'test-token', got '%s'", w.Token)
	}

	// Try registering again, should not fail or duplicate
	err = worker.SelfRegister(ctx)
	if err != nil {
		t.Fatalf("Second SelfRegister failed: %v", err)
	}
}
