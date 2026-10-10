package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/testutil"
)

type decidedStore struct {
	testutil.BaseMockStorage
	status  string
	updates atomic.Int32
}

func (s *decidedStore) GetApproval(context.Context, string) (storage.Approval, error) {
	return storage.Approval{ID: "a1", WorkflowID: "wf", NodeID: "agent", Status: s.status}, nil
}

func (s *decidedStore) UpdateApprovalStatus(context.Context, string, string, string, string, map[string]any) error {
	s.updates.Add(1)
	return nil
}

// A decision resumes the workflow. Deciding an approval that was already
// decided would resume it a second time: the message is delivered twice, and
// an AI agent's approved write tool runs twice.
func TestADecidedApprovalCannotBeDecidedAgain(t *testing.T) {
	for _, status := range []string{"approved", "rejected"} {
		for _, decide := range []string{"approve", "reject"} {
			t.Run(status+"/"+decide, func(t *testing.T) {
				store := &decidedStore{status: status}
				h := NewApprovalHandler(&handlers.Handler{Storage: store})
				mux := http.NewServeMux()
				mux.HandleFunc("POST /api/approvals/{id}/approve", h.ApproveApproval)
				mux.HandleFunc("POST /api/approvals/{id}/reject", h.RejectApproval)

				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/approvals/a1/"+decide, strings.NewReader(`{}`)))

				if rec.Code != http.StatusConflict {
					t.Fatalf("status = %d, want 409; body %s", rec.Code, rec.Body.String())
				}
				if n := store.updates.Load(); n != 0 {
					t.Fatalf("an already-%s approval was updated %d time(s)", status, n)
				}
			})
		}
	}
}

type racedStore struct {
	testutil.BaseMockStorage
	gets atomic.Int32
}

func (s *racedStore) GetApproval(context.Context, string) (storage.Approval, error) {
	s.gets.Add(1)
	return storage.Approval{ID: "a1", WorkflowID: "wf", NodeID: "agent", Status: "pending"}, nil
}

// Another decision won between this request's read and its update.
func (s *racedStore) UpdateApprovalStatus(context.Context, string, string, string, string, map[string]any) error {
	return storage.ErrApprovalDecided
}

// The status check above cannot see a decision that lands between the read
// and the update. The store refuses that update, and the loser must answer 409
// without resuming the workflow.
func TestALosingConcurrentDecisionIsAConflictAndDoesNotResume(t *testing.T) {
	store := &racedStore{}
	h := NewApprovalHandler(&handlers.Handler{Storage: store})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/approvals/{id}/approve", h.ApproveApproval)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/approvals/a1/approve", strings.NewReader(`{}`)))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body %s", rec.Code, rec.Body.String())
	}
	time.Sleep(50 * time.Millisecond)
	if n := store.gets.Load(); n != 1 {
		t.Fatalf("approval read %d times, want 1: a resume was started", n)
	}
}
