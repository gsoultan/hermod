//go:build integration
// +build integration

package test

import (
	"context"
	"database/sql"
	"github.com/gsoultan/Hermod/internal/engine/registry"
	"github.com/gsoultan/Hermod/internal/engine/worker"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/internal/storage"
	sqlstorage "github.com/gsoultan/Hermod/internal/storage/sql"
	"github.com/gsoultan/Hermod/pkg/comm/message"
	"github.com/gsoultan/Hermod/pkg/comm/source/webhook"
	_ "modernc.org/sqlite"
)

// TestTwoWorkerLeaseFailover verifies that only one worker processes a workflow at a time
// and after the first worker stops and lease TTL expires, the second worker steals the lease
// and continues processing. It also asserts no duplicate rows are stored thanks to idempotency.
// leaseTTL is short so the test does not take a minute, but it is the one
// timing constant the failover genuinely depends on, so it is named.
const leaseTTL = 3 * time.Second

// readyTimeout bounds the waits for something that should simply happen — a
// worker syncing, a lease being stolen. It is deliberately far longer than any
// of them need. These are liveness assertions, not latency ones: a regression
// means the thing never happens, and a tight bound only converts a busy machine
// into a red build.
const readyTimeout = 60 * time.Second

func TestTwoWorkerLeaseFailover(t *testing.T) {
	if os.Getenv("HERMOD_INTEGRATION") != "1" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 to run")
	}

	ctx := t.Context()

	// --- Platform storage (SQLite on disk) ---
	//
	// In a temp directory, not the package directory. These databases were
	// created as bare relative paths and removed by name at the end, which
	// leaves the SQLite WAL sidecars (-wal, -shm) behind: a run that ended
	// early left state that the next run opened, and the failures that caused
	// looked like flakiness in the engine rather than debris on disk.
	// t.TempDir is removed wholesale, sidecars included.
	dir := t.TempDir()
	const pragmas = "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)"

	stateDB, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "e2e_state.db")+pragmas)
	if err != nil {
		t.Fatalf("open state db: %v", err)
	}
	defer stateDB.Close()

	store := sqlstorage.NewSQLStorage(stateDB, "sqlite")
	if err := store.Init(ctx); err != nil {
		t.Fatalf("init store: %v", err)
	}

	// --- Sink SQLite DB ---
	sinkPath := filepath.Join(dir, "e2e_sink.db")
	sinkDB, err := sql.Open("sqlite", sinkPath+pragmas)
	if err != nil {
		t.Fatalf("open sink db: %v", err)
	}
	defer sinkDB.Close()

	table := "e2e_msgs"
	if _, err := sinkDB.Exec("CREATE TABLE IF NOT EXISTS " + table + " (id TEXT PRIMARY KEY, data BLOB NOT NULL)"); err != nil {
		t.Fatalf("create sink table: %v", err)
	}

	// --- registry.Registry ---
	reg := registry.NewRegistry(store)

	// --- Source and Sink records ---
	src := storage.Source{
		ID:     uuid.New().String(),
		Name:   "e2e-webhook",
		Type:   "webhook",
		Active: true,
		Config: map[string]string{"path": "/e2e"},
	}
	if err := store.CreateSource(ctx, src); err != nil {
		t.Fatalf("create source: %v", err)
	}

	snk := storage.Sink{
		ID:     uuid.New().String(),
		Name:   "e2e-sqlite",
		Type:   "sqlite",
		Active: true,
		Config: map[string]string{"path": sinkPath},
	}
	if err := store.CreateSink(ctx, snk); err != nil {
		t.Fatalf("create sink: %v", err)
	}

	// --- Workflow definition: src -> sink ---
	wf := storage.Workflow{
		ID:     uuid.New().String(),
		Name:   "e2e-wf",
		Active: true,
		Nodes: []storage.WorkflowNode{
			{ID: "src1", Type: "source", RefID: src.ID},
			{ID: "snk1", Type: "sink", RefID: snk.ID},
		},
		Edges: []storage.WorkflowEdge{{ID: "e1", SourceID: "src1", TargetID: "snk1"}},
	}
	if err := store.CreateWorkflow(ctx, wf); err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	// --- Workers ---
	w1 := worker.NewWorker(store, reg)
	w1.SetWorkerConfig(0, 1, "worker-1", "")
	w1.SetLeaseTTL(int(leaseTTL.Seconds()))
	w1.SetSyncInterval(500 * time.Millisecond)

	w2 := worker.NewWorker(store, reg)
	w2.SetWorkerConfig(0, 1, "worker-2", "")
	w2.SetLeaseTTL(int(leaseTTL.Seconds()))
	w2.SetSyncInterval(500 * time.Millisecond)

	// Turn off load shedding for both workers. This test is about whether a
	// lease is stolen, and admission control is a separate mechanism with its
	// own tests. Left on, a busy machine makes the worker taking over refuse the
	// workflow, and the failure surfaces as the missing webhook further down —
	// blaming the engine for the host being loaded, which is how this test
	// failed inside a full parallel run.
	w1.SetAdmissionThresholds(1, 1)
	w2.SetAdmissionThresholds(1, 1)

	// Start worker 1
	ctx1, cancel1 := context.WithCancel(t.Context())
	defer cancel1()
	// Surface the worker's own failure. Discarding this error meant that when
	// the worker did not come up, the test blamed whatever it was waiting for
	// next -- "no webhook registered" -- rather than saying why.
	// One channel per worker, not one shared between them. Worker 1 is stopped
	// on purpose partway through, so its exit is expected from that point on;
	// with a shared channel the readiness check below would read that expected
	// exit and report it as worker 2 having died.
	w1Err := make(chan error, 1)
	w2Err := make(chan error, 1)
	go func() { w1Err <- w1.Start(ctx1) }()

	// Wait for the worker to have registered its webhook source.
	//
	// This was a fixed 500ms sleep, which is a guess about how long a sync
	// takes rather than a fact about it. Under load -- a full `go test ./...`
	// with every package competing for CPU -- the sync had not finished and the
	// dispatch below failed with "no webhook registered for path: /e2e". Waiting
	// for the condition instead of for the clock makes the test deterministic
	// and, when the sync genuinely breaks, it fails for that reason rather than
	// intermittently.
	dispatchWhenReady := func(t *testing.T, path string, m hermod.Message, workerErr <-chan error) {
		t.Helper()
		deadline := time.Now().Add(readyTimeout)
		for {
			select {
			case werr := <-workerErr:
				if werr != nil {
					t.Fatalf("the worker exited instead of registering %s: %v", path, werr)
				}
				t.Fatalf("the worker returned before registering %s", path)
			default:
			}

			err := webhook.Dispatch(path, m)
			if err == nil {
				return
			}
			if !strings.Contains(err.Error(), "no webhook registered") || time.Now().After(deadline) {
				t.Fatalf("dispatch to %s after waiting %s for the worker to register it: %v",
					path, readyTimeout, err)
			}
			time.Sleep(25 * time.Millisecond)
		}
	}

	// Inject two messages
	m1 := message.AcquireMessage()
	m1.SetID("id-1")
	m1.SetTable(table)
	m1.SetAfter([]byte(`{"n":1}`))
	defer message.ReleaseMessage(m1)
	m2 := message.AcquireMessage()
	m2.SetID("id-2")
	m2.SetTable(table)
	m2.SetAfter([]byte(`{"n":2}`))
	defer message.ReleaseMessage(m2)
	dispatchWhenReady(t, "/e2e", m1, w1Err)
	dispatchWhenReady(t, "/e2e", m2, w1Err)

	// Wait for processing
	awaitRows(t, sinkDB, table, 2, 5*time.Second)

	// Duplicate id-1 should not create new row (upsert)
	m1d := message.AcquireMessage()
	m1d.SetID("id-1")
	m1d.SetTable(table)
	m1d.SetAfter([]byte(`{"n":1}`))
	defer message.ReleaseMessage(m1d)
	// Not a discarded error: if this dispatch silently failed, the row count
	// below would still be 2 and the test would pass without ever checking that
	// a duplicate is suppressed.
	dispatchWhenReady(t, "/e2e", m1d, w1Err)
	awaitRows(t, sinkDB, table, 2, 2*time.Second)

	// Simulate crash of worker 1
	cancel1()

	// Start worker 2; it should steal the lease after TTL
	ctx2, cancel2 := context.WithCancel(t.Context())
	defer cancel2()
	go func() { w2Err <- w2.Start(ctx2) }()

	// Wait for worker 2 to steal the lease and restart the engine.
	//
	// The lease TTL is 3s, so failover cannot be quicker than that; the sleep
	// below only skips polling during a window where success is impossible. The
	// deadline after it is deliberately generous. What this test is for is
	// "does the lease get stolen at all" -- a regression there means failover is
	// broken and the workflow stops dead. Tying it to a tight deadline instead
	// measures how loaded the machine is, which is how this failed under a full
	// parallel test run rather than because of anything in the engine.
	time.Sleep(leaseTTL)

	waitDeadline := time.Now().Add(readyTimeout)
	for !reg.IsEngineRunning(wf.ID) {
		if time.Now().After(waitDeadline) {
			t.Fatalf("worker 2 never took over the workflow after worker 1 stopped; "+
				"the lease was not stolen within %s of the %s TTL expiring, so the "+
				"workflow would stay stopped", 60*time.Second, leaseTTL)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Inject more messages and a duplicate
	m3 := message.AcquireMessage()
	m3.SetID("id-3")
	m3.SetTable(table)
	m3.SetAfter([]byte(`{"n":3}`))
	defer message.ReleaseMessage(m3)
	m4 := message.AcquireMessage()
	m4.SetID("id-2")
	m4.SetTable(table)
	m4.SetAfter([]byte(`{"n":22}`)) // duplicate id-2
	defer message.ReleaseMessage(m4)
	// Wait for worker 2 to register the source, exactly as worker 1's dispatches
	// do. IsEngineRunning above reports that the engine has started, which is
	// not the same instant as the webhook path being registered — so these two
	// dispatches raced the takeover they had just waited for, and failed with
	// "no webhook registered for path: /e2e" whenever the machine was loaded
	// enough to lose that race.
	dispatchWhenReady(t, "/e2e", m3, w2Err)
	dispatchWhenReady(t, "/e2e", m4, w2Err)

	// Expect only 3 distinct rows due to idempotency
	awaitRows(t, sinkDB, table, 3, 6*time.Second)
}

func awaitRows(t *testing.T, db *sql.DB, table string, want int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for {
		var cnt int
		_ = db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&cnt)
		if cnt == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %d rows, last=%d", want, cnt)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
