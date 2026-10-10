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
