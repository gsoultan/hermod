package sql

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/user/hermod/internal/storage"
	_ "modernc.org/sqlite"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:memdb1?mode=memory&cache=shared&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	return db
}

func TestWorkflowLease_AcquireRenewRelease(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	defer db.Close()

	s := NewSQLStorage(db, "sqlite").(*sqlStorage)
	if err := s.Init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}

	wf := storage.Workflow{ID: "wf1", Name: "wf1", Active: true}
	if err := s.CreateWorkflow(ctx, wf); err != nil {
		t.Fatalf("create wf: %v", err)
	}

	// Acquire by w1
	ok, err := s.AcquireWorkflowLease(ctx, "wf1", "w1", 30)
	if err != nil || !ok {
		t.Fatalf("acquire w1 failed: ok=%v err=%v", ok, err)
	}

	// Another owner cannot acquire while not expired
	ok, err = s.AcquireWorkflowLease(ctx, "wf1", "w2", 30)
	if err != nil {
		t.Fatalf("acquire w2 err: %v", err)
	}
	if ok {
		t.Fatalf("acquire w2 should not succeed while lease active")
	}

	// Renew by w1
	ok, err = s.RenewWorkflowLease(ctx, "wf1", "w1", 30)
	if err != nil || !ok {
		t.Fatalf("renew w1 failed: ok=%v err=%v", ok, err)
	}

	// Force expiry by setting lease_until in the past
	past := time.Now().Add(-2 * time.Second).UTC()
	if _, err := db.ExecContext(ctx, "UPDATE workflows SET lease_until = ? WHERE id = ?", past, "wf1"); err != nil {
		t.Fatalf("set past lease: %v", err)
	}

	// Now w2 can steal
	ok, err = s.AcquireWorkflowLease(ctx, "wf1", "w2", 30)
	if err != nil || !ok {
		t.Fatalf("steal by w2 failed: ok=%v err=%v", ok, err)
	}

	// Release by w2
	if err := s.ReleaseWorkflowLease(ctx, "wf1", "w2"); err != nil {
		t.Fatalf("release w2: %v", err)
	}

	// w1 can acquire again
	ok, err = s.AcquireWorkflowLease(ctx, "wf1", "w1", 30)
	if err != nil || !ok {
		t.Fatalf("reacquire w1 failed: ok=%v err=%v", ok, err)
	}
}
