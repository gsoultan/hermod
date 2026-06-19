package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/user/hermod/internal/storage"
)

func TestShutdownWorker(t *testing.T) {
	tests := []struct {
		name        string
		role        storage.Role
		getErr      error
		wantStatus  int
		wantDrained bool
	}{
		{"Forbidden for non-admin", storage.RoleViewer, nil, http.StatusForbidden, false},
		{"Not found", storage.RoleAdministrator, storage.ErrNotFound, http.StatusNotFound, false},
		{"Draining for admin", storage.RoleAdministrator, nil, http.StatusAccepted, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &workerStartMockStorage{
				worker: storage.Worker{ID: "w-1", Name: "worker-1", Token: "secret"},
				getErr: tc.getErr,
			}
			h := &Handler{Storage: store, LogStorage: store}

			req := httptest.NewRequest(http.MethodPost, "/api/workers/w-1/shutdown", nil)
			req.SetPathValue("id", "w-1")
			ctx := context.WithValue(req.Context(), UserContextKey, &storage.User{ID: "u-1", Username: "admin", Role: tc.role})
			req = req.WithContext(ctx)

			w := httptest.NewRecorder()
			h.ShutdownWorker(w, req)

			if w.Code != tc.wantStatus {
				t.Errorf("%s: expected status %d, got %d", tc.name, tc.wantStatus, w.Code)
			}
			if got := h.IsWorkerDraining("w-1"); got != tc.wantDrained {
				t.Errorf("%s: expected draining=%v, got %v", tc.name, tc.wantDrained, got)
			}
		})
	}
}

// TestShutdownWorkerSurfacedToGetWorker verifies a draining request is reflected
// back to a polling worker via GetWorker so it can begin its graceful shutdown.
func TestShutdownWorkerSurfacedToGetWorker(t *testing.T) {
	store := &workerStartMockStorage{worker: storage.Worker{ID: "w-1", Name: "worker-1", Token: "secret"}}
	h := &Handler{Storage: store, LogStorage: store}

	h.MarkWorkerDraining("w-1")

	req := httptest.NewRequest(http.MethodGet, "/api/workers/w-1", nil)
	req.SetPathValue("id", "w-1")
	w := httptest.NewRecorder()
	h.GetWorker(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if body := w.Body.String(); !strings.Contains(body, "\"draining\":true") {
		t.Errorf("expected draining flag in response, got %s", body)
	}

	// After deregistration the pending request must be cleared.
	delReq := httptest.NewRequest(http.MethodDelete, "/api/workers/w-1", nil)
	delReq.SetPathValue("id", "w-1")
	h.DeleteWorker(httptest.NewRecorder(), delReq)
	if h.IsWorkerDraining("w-1") {
		t.Error("expected draining flag cleared after delete")
	}
}
