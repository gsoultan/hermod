package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/user/hermod/internal/storage"
)

type workerStartMockStorage struct {
	storage.Storage
	worker storage.Worker
	getErr error
}

func (m *workerStartMockStorage) GetWorker(_ context.Context, _ string) (storage.Worker, error) {
	if m.getErr != nil {
		return storage.Worker{}, m.getErr
	}
	return m.worker, nil
}

func (m *workerStartMockStorage) DeleteWorker(_ context.Context, _ string) error { return nil }

func (m *workerStartMockStorage) CreateLog(_ context.Context, _ storage.Log) error { return nil }

func (m *workerStartMockStorage) CreateAuditLog(_ context.Context, _ storage.AuditLog) error {
	return nil
}

func TestStartWorker(t *testing.T) {
	// Stub the process spawner so tests never launch a real binary.
	originalSpawn := spawnWorkerProcess
	defer func() { spawnWorkerProcess = originalSpawn }()
	spawnWorkerProcess = func(_ string, _ storage.Worker, _ string) (int, error) {
		return 4242, nil
	}

	tests := []struct {
		name       string
		role       storage.Role
		getErr     error
		wantStatus int
	}{
		{"Forbidden for non-admin", storage.RoleViewer, nil, http.StatusForbidden},
		{"Not found", storage.RoleAdministrator, storage.ErrNotFound, http.StatusNotFound},
		{"Started for admin", storage.RoleAdministrator, nil, http.StatusAccepted},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &workerStartMockStorage{
				worker: storage.Worker{ID: "w-1", Name: "worker-1", Token: "secret", Host: "localhost", Port: 3000},
				getErr: tc.getErr,
			}
			h := &Handler{Storage: store, LogStorage: store}

			req := httptest.NewRequest(http.MethodPost, "/api/workers/w-1/start", nil)
			req.SetPathValue("id", "w-1")
			ctx := context.WithValue(req.Context(), UserContextKey, &storage.User{ID: "u-1", Username: "admin", Role: tc.role})
			req = req.WithContext(ctx)

			w := httptest.NewRecorder()
			h.StartWorker(w, req)

			if w.Code != tc.wantStatus {
				t.Errorf("%s: expected status %d, got %d", tc.name, tc.wantStatus, w.Code)
			}
		})
	}
}
