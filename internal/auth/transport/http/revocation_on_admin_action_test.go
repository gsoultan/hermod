package http

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
)

// ---------------------------------------------------------------------------
// Revocation on administrative action.
//
// The auth middleware builds the request's user from the token's claims and
// never reads the database, which is what keeps an authenticated request free
// of I/O. The consequence is that the claims are the authority: until the token
// expires, the role, the vhosts and the account's very existence are whatever
// they were at login.
//
// So every administrative action that invalidates those claims has to revoke.
// Deleting a user whose session keeps working is the clearest case — the
// account is gone and the token still authenticates — but a demotion is the
// same bug with a smaller blast radius.
//
// The counterpart matters just as much: editing a full name must not log
// anybody out, or routine profile edits become outages and the feature gets
// turned off.
// ---------------------------------------------------------------------------

type adminActionStore struct {
	storage.Storage
	user    storage.User
	deleted []string
	updated []storage.User
}

func (s *adminActionStore) GetUser(_ context.Context, id string) (storage.User, error) {
	if id != s.user.ID {
		return storage.User{}, storage.ErrNotFound
	}
	return s.user, nil
}

func (s *adminActionStore) UpdateUser(_ context.Context, u storage.User) error {
	s.updated = append(s.updated, u)
	s.user = u
	return nil
}

func (s *adminActionStore) DeleteUser(_ context.Context, id string) error {
	s.deleted = append(s.deleted, id)
	return nil
}

func (s *adminActionStore) CreateLog(context.Context, storage.Log) error { return nil }

func (s *adminActionStore) CreateAuditLog(context.Context, storage.AuditLog) error { return nil }

// targetUser is the account every test here acts on.
const targetUser = "u1"

// adminRequest builds a request made by an administrator, targeting targetUser.
func adminRequest(t *testing.T, method, body string) *http.Request {
	t.Helper()
	admin := &storage.User{ID: "admin-1", Username: "root", Role: storage.RoleAdministrator}
	ctx := context.WithValue(context.Background(), handlers.UserContextKey, admin)
	r := httptest.NewRequestWithContext(ctx, method, "/api/users/"+targetUser, bytes.NewBufferString(body))
	r.SetPathValue("id", targetUser)
	return r
}

// targetSession is a session held by the user being acted on, started before
// the action.
func targetSession(userID string) handlers.SessionClaims {
	return handlers.SessionClaims{
		UserID:       userID,
		TokenID:      "target-session",
		SessionStart: time.Now().Add(-time.Minute),
	}
}

func newAdminHandler(u storage.User) (*AuthHandler, *adminActionStore) {
	store := &adminActionStore{user: u}
	return &AuthHandler{Handler: &handlers.Handler{Storage: store, LogStorage: store}}, store
}

// TestDeletingAUserEndsTheirSessions. The account is gone; the token must stop
// working. Without this the middleware keeps authenticating a deleted user from
// claims alone, for the full life of the token.
func TestDeletingAUserEndsTheirSessions(t *testing.T) {
	h, store := newAdminHandler(storage.User{ID: "u1", Username: "alice", Role: storage.RoleViewer})

	rec := httptest.NewRecorder()
	h.DeleteUser(rec, adminRequest(t, http.MethodDelete, ""))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete failed: %d", rec.Code)
	}
	if len(store.deleted) != 1 {
		t.Fatalf("the user was not deleted")
	}
	if !h.SessionRevoker().IsRevoked(targetSession("u1")) {
		t.Error("a deleted user's session still authenticates; " +
			"the middleware builds the user from claims and never checks the account still exists")
	}
}

// TestChangingARoleEndsTheirSessions. A demotion that leaves the old session
// holding administrator claims has not demoted anybody until the token expires.
func TestChangingARoleEndsTheirSessions(t *testing.T) {
	h, _ := newAdminHandler(storage.User{ID: "u1", Username: "alice", Role: storage.RoleAdministrator})

	rec := httptest.NewRecorder()
	h.UpdateUser(rec, adminRequest(t, http.MethodPut,
		`{"role":"Viewer"}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("update failed: %d — %s", rec.Code, rec.Body.String())
	}
	if !h.SessionRevoker().IsRevoked(targetSession("u1")) {
		t.Error("the demoted user's session still carries administrator claims")
	}
}

// TestChangingVHostsEndsTheirSessions. VHosts are carried in the claims and used
// for scoping, so removing one has to end the sessions that still list it.
func TestChangingVHostsEndsTheirSessions(t *testing.T) {
	h, _ := newAdminHandler(storage.User{
		ID: "u1", Username: "alice", Role: storage.RoleViewer, VHosts: []string{"prod", "staging"}})

	rec := httptest.NewRecorder()
	h.UpdateUser(rec, adminRequest(t, http.MethodPut,
		`{"vhosts":["staging"]}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("update failed: %d — %s", rec.Code, rec.Body.String())
	}
	if !h.SessionRevoker().IsRevoked(targetSession("u1")) {
		t.Error("the user's session still lists a vhost they were just removed from")
	}
}

// TestUpdateUserPasswordAlsoRevokes. There are two paths that set a password;
// only one of them revoked, which makes the guarantee depend on which endpoint
// the caller happened to use.
func TestUpdateUserPasswordAlsoRevokes(t *testing.T) {
	h, _ := newAdminHandler(storage.User{ID: "u1", Username: "alice", Role: storage.RoleViewer})

	rec := httptest.NewRecorder()
	h.UpdateUser(rec, adminRequest(t, http.MethodPut,
		`{"password":"a-new-password"}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("update failed: %d — %s", rec.Code, rec.Body.String())
	}
	if !h.SessionRevoker().IsRevoked(targetSession("u1")) {
		t.Error("changing a password through PUT /api/users/{id} left every session alive, " +
			"while the dedicated password endpoint ends them")
	}
}

// TestACosmeticEditDoesNotEndSessions is the guard that keeps the rule from
// being blunt. If correcting a typo in someone's full name logs them out, the
// feature is an outage generator and somebody disables it.
func TestACosmeticEditDoesNotEndSessions(t *testing.T) {
	h, _ := newAdminHandler(storage.User{
		ID: "u1", Username: "alice", Role: storage.RoleViewer, VHosts: []string{"prod"}})

	rec := httptest.NewRecorder()
	h.UpdateUser(rec, adminRequest(t, http.MethodPut,
		`{"full_name":"Alice Example","email":"alice@example.com"}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("update failed: %d — %s", rec.Code, rec.Body.String())
	}
	if h.SessionRevoker().IsRevoked(targetSession("u1")) {
		t.Error("editing a display name logged the user out; " +
			"revocation must follow the claims that changed, not any write at all")
	}
}

// TestAnotherUserIsUnaffected: none of these actions may touch a bystander.
func TestAnotherUserIsUnaffected(t *testing.T) {
	h, _ := newAdminHandler(storage.User{ID: "u1", Username: "alice", Role: storage.RoleAdministrator})

	rec := httptest.NewRecorder()
	h.UpdateUser(rec, adminRequest(t, http.MethodPut, `{"role":"Viewer"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("update failed: %d", rec.Code)
	}

	if h.SessionRevoker().IsRevoked(targetSession("u2")) {
		t.Error("acting on one user ended a different user's sessions")
	}
}
