package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gsoultan/hermod/internal/api/handlers"
)

// The self-healing proposals API is mounted behind authentication, and
// deciding a proposal needs the Editor role.
func TestTheServerMountsSelfHealingProposalsBehindAuth(t *testing.T) {
	const secret = "proposal-route-secret"
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "db_config.yaml"),
		[]byte("type: sqlite\nconn: \"file::memory:\"\njwt_secret: "+secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERMOD_CONFIG_DIR", dir)
	t.Setenv("HERMOD_JWT_SECRET", secret)

	s := &Server{Handler: &handlers.Handler{Storage: &mockStorage{}}}
	srv := httptest.NewServer(s.Routes())
	defer srv.Close()

	call := func(method, path, role string) int {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if role != "" {
			token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
				"id": "u1", "username": "alice", "role": role, "exp": time.Now().Add(time.Hour).Unix(),
			}).SignedString([]byte(secret))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	if code := call(http.MethodGet, "/api/workflows/wf/proposals", ""); code != http.StatusUnauthorized {
		t.Errorf("anonymous list: %d, want 401", code)
	}
	if code := call(http.MethodPost, "/api/workflows/wf/proposals/p/approve", ""); code != http.StatusUnauthorized {
		t.Errorf("anonymous approve: %d, want 401", code)
	}
	if code := call(http.MethodPost, "/api/workflows/wf/proposals/p/approve", "Viewer"); code != http.StatusForbidden {
		t.Errorf("Viewer approve: %d, want 403", code)
	}
	if code := call(http.MethodPost, "/api/workflows/wf/proposals/p/reject", "Viewer"); code != http.StatusForbidden {
		t.Errorf("Viewer reject: %d, want 403", code)
	}
}
