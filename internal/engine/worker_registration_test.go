package engine

import (
	"context"
	"testing"

	"github.com/user/hermod/internal/storage"
)

type mockRegStorage struct {
	BaseMockStorage
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

func (m *mockRegStorage) ListWorkers(ctx context.Context, filter storage.CommonFilter) ([]storage.Worker, int, error) {
	var workers []storage.Worker
	for _, w := range m.workers {
		workers = append(workers, w)
	}
	return workers, len(workers), nil
}

func (m *mockRegStorage) DeleteWorker(ctx context.Context, id string) error {
	delete(m.workers, id)
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

func TestWorker_SelfRegister_Cleanup(t *testing.T) {
	store := &mockRegStorage{workers: make(map[string]storage.Worker)}
	registry := NewRegistry(store)

	// Add a stale worker with same host/port but different GUID
	staleGUID := "stale-guid"
	store.workers[staleGUID] = storage.Worker{
		ID:   staleGUID,
		Host: "localhost",
		Port: 8081,
	}

	worker := NewWorker(store, registry)
	worker.SetWorkerConfig(0, 1, "new-guid", "test-token")
	worker.SetRegistrationInfo("New Worker", "localhost", 8081, "A new worker")

	ctx := t.Context()
	err := worker.SelfRegister(ctx)
	if err != nil {
		t.Fatalf("SelfRegister failed: %v", err)
	}

	// Check that stale worker is gone
	_, err = store.GetWorker(ctx, staleGUID)
	if err != storage.ErrNotFound {
		t.Errorf("Expected stale worker to be deleted, but it still exists")
	}

	// Check that new worker is registered
	w, err := store.GetWorker(ctx, "new-guid")
	if err != nil {
		t.Errorf("Expected new worker to be registered, but got error: %v", err)
	}
	if w.ID != "new-guid" {
		t.Errorf("Expected worker ID 'new-guid', got '%s'", w.ID)
	}
}

func TestWorker_SelfRegister_CleanupByName(t *testing.T) {
	store := &mockRegStorage{workers: make(map[string]storage.Worker)}
	registry := NewRegistry(store)

	// Add a stale worker with same Name but different host/port and different GUID
	staleGUID := "stale-guid-name"
	store.workers[staleGUID] = storage.Worker{
		ID:   staleGUID,
		Name: "my-stable-hostname",
		Host: "192.168.1.1",
		Port: 9000,
	}

	worker := NewWorker(store, registry)
	worker.SetWorkerConfig(0, 1, "new-guid", "test-token")
	worker.SetRegistrationInfo("my-stable-hostname", "192.168.1.2", 8081, "A new worker")

	ctx := t.Context()
	err := worker.SelfRegister(ctx)
	if err != nil {
		t.Fatalf("SelfRegister failed: %v", err)
	}

	// Check that stale worker is gone
	_, err = store.GetWorker(ctx, staleGUID)
	if err != storage.ErrNotFound {
		t.Errorf("Expected stale worker with same name to be deleted, but it still exists")
	}

	// Check that new worker is registered
	_, err = store.GetWorker(ctx, "new-guid")
	if err != nil {
		t.Errorf("Expected new worker to be registered, but got error: %v", err)
	}
}
