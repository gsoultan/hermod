package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Verifies that websocket endpoints are not publicly accessible without auth.
func TestWebsocket_Unauthorized_Without_Token(t *testing.T) {
	s := NewServer(nil, &fakeStorage{}, nil, nil)
	h := s.Routes()

	// Attempt to access WS status without Authorization
	req := httptest.NewRequest(http.MethodGet, "/api/ws/status", nil)
	// Simulate upgrade attempt headers (middleware should reject before upgrade)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 unauthorized for WS endpoint without token, got %d", rr.Code)
	}
}
