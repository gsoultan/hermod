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

// A user limited to some vhosts must not see approvals raised by workflows in
// other vhosts: an approval carries the record and, for an AI agent, the write
// it wants to make.
func TestListApprovals_VHostsLimitsToThoseWorkflows(t *testing.T) {
	s, _ := newTraceStorage(t)
	for _, wf := range []storage.Workflow{
		{ID: "wa", Name: "wa", VHost: "a"},
		{ID: "wb", Name: "wb", VHost: "b"},
		{ID: "wd", Name: "wd", VHost: ""},
	} {
		if err := s.CreateWorkflow(t.Context(), wf); err != nil {
			t.Fatalf("create workflow: %v", err)
		}
	}
	for _, id := range []string{"wa", "wb", "wd"} {
		if err := s.CreateApproval(t.Context(), storage.Approval{
			ID: "ap-" + id, WorkflowID: id, NodeID: "n", Status: "pending", CreatedAt: time.Now(),
		}); err != nil {
			t.Fatalf("create approval: %v", err)
		}
	}

	got := func(vhosts []string) map[string]bool {
		apps, total, err := s.ListApprovals(t.Context(), storage.ApprovalFilter{VHosts: vhosts})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		ids := map[string]bool{}
		for _, a := range apps {
			ids[a.WorkflowID] = true
		}
		if total != len(apps) {
			t.Fatalf("total = %d, listed %d", total, len(apps))
		}
		return ids
	}

	if ids := got(nil); len(ids) != 3 {
		t.Fatalf("no limit: %v", ids)
	}
	if ids := got([]string{"a"}); len(ids) != 2 || !ids["wa"] || !ids["wd"] {
		t.Fatalf("vhost a: %v (want wa and the shared default wd)", ids)
	}
	if ids := got([]string{}); len(ids) != 1 || !ids["wd"] {
		t.Fatalf("no vhosts: %v (want only the shared default)", ids)
	}
}
