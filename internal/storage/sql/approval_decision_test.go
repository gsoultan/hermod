package sql

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

// Deciding an approval resumes its workflow, so only one decision may win. The
// handler's status check cannot close the race between two decisions that
// arrive together; the update itself has to.
func TestUpdateApprovalStatus_OnlyThePendingApprovalCanBeDecided(t *testing.T) {
	s, _ := newTraceStorage(t)
	if err := s.CreateApproval(t.Context(), storage.Approval{
		ID: "a1", WorkflowID: "wf", NodeID: "n", Status: "pending", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	var wins atomic.Int32
	var wg sync.WaitGroup
	for _, status := range []string{"approved", "rejected", "approved", "rejected"} {
		wg.Go(func() {
			err := s.UpdateApprovalStatus(t.Context(), "a1", status, "u", "", nil)
			switch {
			case err == nil:
				wins.Add(1)
			case errors.Is(err, storage.ErrApprovalDecided):
			default:
				t.Errorf("update %s: %v", status, err)
			}
		})
	}
	wg.Wait()

	if n := wins.Load(); n != 1 {
		t.Fatalf("%d decisions won, want exactly 1", n)
	}
}

func TestUpdateApprovalStatus_MissingApprovalIsNotFound(t *testing.T) {
	s, _ := newTraceStorage(t)
	err := s.UpdateApprovalStatus(t.Context(), "nope", "approved", "u", "", nil)
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
