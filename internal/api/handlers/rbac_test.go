package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/user/hermod/internal/storage"
)

// ---------------------------------------------------------------------------
// Authorization.
//
// 62 routes are guarded (53 EditorOnly, 9 AdminOnly) and almost none of that
// was covered: four test files touched authorization at all, none of them the
// middleware every one of those routes funnels through. These tests go at the
// decision itself rather than replaying 62 routes with four tokens each —
// exhaustive on the logic, and not brittle against route churn. A separate
// structural test catches the other half of the problem: a new route that
// forgets a guard entirely.
// ---------------------------------------------------------------------------

// rbacStorage controls the two things RbacMiddleware consults through
// IsFirstRun: whether any user exists, and whether listing them errors.
type rbacStorage struct {
	storage.Storage
	userCount int
	listErr   error
}

func (m *rbacStorage) ListUsers(context.Context, storage.CommonFilter) ([]storage.User, int, error) {
	if m.listErr != nil {
		return nil, 0, m.listErr
	}
	return nil, m.userCount, nil
}

// withUser attaches an authenticated identity the way the auth middleware does.
func withUser(r *http.Request, role storage.Role) *http.Request {
	u := &storage.User{ID: "u-1", Username: "tester", Role: role}
	return r.WithContext(context.WithValue(r.Context(), UserContextKey, u))
}

// TestRbacMiddlewareDecisions is the full truth table for the guard every
// protected route uses.
func TestRbacMiddlewareDecisions(t *testing.T) {
	const (
		anonymous storage.Role = "" // no identity in context at all
	)

	cases := []struct {
		name     string
		required storage.Role
		actor    storage.Role
		want     int
	}{
		// Anonymous callers must never reach a guarded handler.
		{"anonymous vs editor-only", storage.RoleEditor, anonymous, http.StatusUnauthorized},
		{"anonymous vs admin-only", storage.RoleAdministrator, anonymous, http.StatusUnauthorized},

		// Viewers may read, never write.
		{"viewer vs editor-only", storage.RoleEditor, storage.RoleViewer, http.StatusForbidden},
		{"viewer vs admin-only", storage.RoleAdministrator, storage.RoleViewer, http.StatusForbidden},

		// Editors may write, but must not reach administration.
		{"editor vs editor-only", storage.RoleEditor, storage.RoleEditor, http.StatusOK},
		{"editor vs admin-only", storage.RoleAdministrator, storage.RoleEditor, http.StatusForbidden},

		// Administrators may do anything.
		{"admin vs editor-only", storage.RoleEditor, storage.RoleAdministrator, http.StatusOK},
		{"admin vs admin-only", storage.RoleAdministrator, storage.RoleAdministrator, http.StatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A configured system: users exist, so the first-run bypass is off.
			h := &Handler{Storage: &rbacStorage{userCount: 1}}

			reached := false
			guarded := h.RbacMiddleware(tc.required)(http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) {
					reached = true
					w.WriteHeader(http.StatusOK)
				}))

			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/guarded", nil)
			if tc.actor != anonymous {
				req = withUser(req, tc.actor)
			}
			rr := httptest.NewRecorder()
			guarded.ServeHTTP(rr, req)

			if rr.Code != tc.want {
				t.Errorf("got HTTP %d, want %d", rr.Code, tc.want)
			}
			if allowed := tc.want == http.StatusOK; reached != allowed {
				t.Errorf("handler reached = %v, want %v", reached, allowed)
			}
		})
	}
}

// TestRbacFirstRunBypassIsNarrow covers the one branch that disables
// authorization outright.
//
// RbacMiddleware short-circuits when IsFirstRun reports true, which is correct
// for a genuinely uninitialised install — the setup wizard has to be reachable
// before an admin exists. It is also the single most dangerous line in the
// authorization path: any condition that wrongly reports "first run" makes
// every guarded route anonymous. In particular a database that is merely
// *unreachable* must not look like an empty one.
func TestRbacFirstRunBypassIsNarrow(t *testing.T) {
	cases := []struct {
		name        string
		store       *rbacStorage
		wantBypass  bool
		explanation string
	}{
		{
			name:        "no users yet: bypass is correct",
			store:       &rbacStorage{userCount: 0},
			wantBypass:  true,
			explanation: "the setup wizard must be reachable before an admin exists",
		},
		{
			name:        "users exist: no bypass",
			store:       &rbacStorage{userCount: 1},
			wantBypass:  false,
			explanation: "a configured system must enforce roles",
		},
		{
			name:       "database unreachable: no bypass",
			store:      &rbacStorage{listErr: errors.New("connection refused")},
			wantBypass: false,
			explanation: "an outage must fail closed; treating it as first-run would open " +
				"every route to anonymous callers exactly when nobody can see the logs",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{Storage: tc.store}

			reached := false
			guarded := h.RbacMiddleware(storage.RoleAdministrator)(http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) {
					reached = true
					w.WriteHeader(http.StatusOK)
				}))

			// Anonymous: without the bypass this is a hard 401.
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/guarded", nil)
			rr := httptest.NewRecorder()
			guarded.ServeHTTP(rr, req)

			if reached != tc.wantBypass {
				t.Errorf("anonymous request reached the handler = %v, want %v: %s",
					reached, tc.wantBypass, tc.explanation)
			}
		})
	}
}

// TestAdminOnlyIsStricterThanEditorOnly guards the two helpers from being
// wired to the same role by accident — a one-word slip that would silently
// promote every administrative route to editor access.
func TestAdminOnlyIsStricterThanEditorOnly(t *testing.T) {
	h := &Handler{Storage: &rbacStorage{userCount: 1}}
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }

	editorReq := withUser(httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/x", nil), storage.RoleEditor)
	rr := httptest.NewRecorder()
	h.AdminOnly(ok).ServeHTTP(rr, editorReq)
	if rr.Code != http.StatusForbidden {
		t.Errorf("AdminOnly admitted an editor with HTTP %d; it is not stricter than EditorOnly", rr.Code)
	}

	editorReq2 := withUser(httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/x", nil), storage.RoleEditor)
	rr2 := httptest.NewRecorder()
	h.EditorOnly(ok).ServeHTTP(rr2, editorReq2)
	if rr2.Code != http.StatusOK {
		t.Errorf("EditorOnly rejected an editor with HTTP %d", rr2.Code)
	}
}

// TestRbacRejectsAMalformedIdentity covers the abuse case: something other than
// a *storage.User under the context key. The type assertion must fail closed
// rather than panic into the recover middleware and return a 500 that looks
// like a bug rather than a rejection.
func TestRbacRejectsAMalformedIdentity(t *testing.T) {
	h := &Handler{Storage: &rbacStorage{userCount: 1}}

	for _, bad := range []any{
		"admin", // a bare string
		storage.User{Role: storage.RoleAdministrator}, // a value, not a pointer
		42,
		nil,
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/x", nil)
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, bad))

		rr := httptest.NewRecorder()
		h.EditorOnly(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}).ServeHTTP(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("identity %#v produced HTTP %d, want 401", bad, rr.Code)
		}
	}
}
