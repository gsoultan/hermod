package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/testutil"
)

// vhostStore holds one pending approval raised by a workflow in vhost "b".
type vhostStore struct {
	testutil.BaseMockStorage
	listFilter storage.ApprovalFilter
	updates    atomic.Int32
}

func (s *vhostStore) GetApproval(context.Context, string) (storage.Approval, error) {
	return storage.Approval{ID: "a1", WorkflowID: "wf-b", NodeID: "agent", Status: "pending"}, nil
}

func (s *vhostStore) GetWorkflow(_ context.Context, id string) (storage.Workflow, error) {
	if id != "wf-b" {
		return storage.Workflow{}, storage.ErrNotFound
	}
	return storage.Workflow{ID: "wf-b", VHost: "b"}, nil
}

func (s *vhostStore) ListApprovals(_ context.Context, f storage.ApprovalFilter) ([]storage.Approval, int, error) {
	s.listFilter = f
	return nil, 0, nil
}

func (s *vhostStore) UpdateApprovalStatus(context.Context, string, string, string, string, map[string]any) error {
	s.updates.Add(1)
	return nil
}

func serveAs(store storage.Storage, user *storage.User, method, path string) *httptest.ResponseRecorder {
	h := NewApprovalHandler(&handlers.Handler{Storage: store})
	mux := http.NewServeMux()
	h.RegisterApprovalRoutes(mux)
	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(`{}`))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, user))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// An approval carries the record and, for an AI agent, the write it wants to
// make; a user of another vhost must neither read nor decide it.
func TestAnotherVHostsApprovalCannotBeReadOrDecided(t *testing.T) {
	user := &storage.User{Username: "eve", Role: storage.RoleEditor, VHosts: []string{"a"}}
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/approvals/a1"},
		{http.MethodPost, "/api/approvals/a1/approve"},
		{http.MethodPost, "/api/approvals/a1/reject"},
	} {
		store := &vhostStore{}
		rec := serveAs(store, user, tc.method, tc.path)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: status = %d, want 404; body %s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
		if n := store.updates.Load(); n != 0 {
			t.Errorf("%s %s: approval updated %d time(s)", tc.method, tc.path, n)
		}
	}
}

func TestTheApprovalListIsLimitedToTheUsersVHosts(t *testing.T) {
	store := &vhostStore{}
	rec := serveAs(store, &storage.User{Role: storage.RoleViewer, VHosts: []string{"a"}}, http.MethodGet, "/api/approvals")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := store.listFilter.VHosts; len(got) != 1 || got[0] != "a" {
		t.Fatalf("filter vhosts = %v, want [a]", got)
	}

	for _, u := range []*storage.User{
		{Role: storage.RoleAdministrator},
		{Role: storage.RoleViewer, VHosts: []string{"*"}},
	} {
		store := &vhostStore{}
		serveAs(store, u, http.MethodGet, "/api/approvals")
		if store.listFilter.VHosts != nil {
			t.Errorf("%s %v: filter vhosts = %v, want no limit", u.Role, u.VHosts, store.listFilter.VHosts)
		}
	}
}
