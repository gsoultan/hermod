package worker

import (
	"context"
	"testing"

	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/internal/testutil"
)

type drainMockStorage struct {
	testutil.BaseMockStorage
	worker storage.Worker
}

func (m *drainMockStorage) GetWorker(_ context.Context, _ string) (storage.Worker, error) {
	return m.worker, nil
}

func TestWorkerRequestShutdown(t *testing.T) {
	w := NewWorker(&drainMockStorage{}, nil)
	w.SetWorkerConfig(0, 1, "worker-1", "token")

	// A mismatching id must not trigger draining.
	w.RequestShutdown("other")
	if w.IsDraining() {
		t.Fatal("worker should not drain for a mismatching id")
	}

	w.RequestShutdown("worker-1")
	if !w.IsDraining() {
		t.Fatal("worker should drain after a matching shutdown request")
	}
}

func TestWorkerPollShutdownRequest(t *testing.T) {
	store := &drainMockStorage{worker: storage.Worker{ID: "worker-1"}}
	w := NewWorker(store, nil)
	w.SetWorkerConfig(0, 1, "worker-1", "token")

	if w.pollShutdownRequest(context.Background()) {
		t.Fatal("worker should not drain when its record is not flagged")
	}

	store.worker.Draining = true
	if !w.pollShutdownRequest(context.Background()) {
		t.Fatal("worker should drain when its record is flagged")
	}
	if !w.IsDraining() {
		t.Fatal("draining state should be latched after polling")
	}
}

func TestWorkerTriggerShutdown(t *testing.T) {
	w := NewWorker(&drainMockStorage{}, nil)
	called := false
	w.SetShutdownFunc(func() { called = true })

	w.TriggerShutdown()
	if !called {
		t.Fatal("expected shutdown func to be invoked")
	}
}
