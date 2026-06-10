package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/user/hermod/internal/storage"
)

type mockStorage struct {
	storage.Storage
}

func (m *mockStorage) ListVHosts(ctx context.Context, filter storage.CommonFilter) ([]storage.VHost, int, error) {
	return []storage.VHost{}, 0, nil
}

func TestVHostsRouting(t *testing.T) {
	mux := http.NewServeMux()
	h := &Handler{
		Storage: &mockStorage{},
	}
	h.RegisterAuthRoutes(mux)

	tests := []struct {
		method string
		path   string
		status int
	}{
		{"GET", "/api/vhosts", http.StatusOK},
		{"GET", "/api/vhosts/", http.StatusOK},
	}

	for _, tt := range tests {
		req := httptest.NewRequest(tt.method, tt.path, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != tt.status {
			t.Errorf("%s %s expected status %d, got %d", tt.method, tt.path, tt.status, w.Code)
		}
	}
}
