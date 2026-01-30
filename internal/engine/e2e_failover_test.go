//go:build integration
// +build integration

package engine

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/user/hermod/internal/storage"
	sqlstorage "github.com/user/hermod/internal/storage/sql"
	"github.com/user/hermod/pkg/message"
	"github.com/user/hermod/pkg/source/webhook"
	_ "modernc.org/sqlite"
)

// TestTwoWorkerLeaseFailover verifies that only one worker processes a workflow at a time
// and after the first worker stops and lease TTL expires, the second worker steals the lease
// and continues processing. It also asserts no duplicate rows are stored thanks to idempotency.
func TestTwoWorkerLeaseFailover(t *testing.T) {
	if os.Getenv("HERMOD_INTEGRATION") != "1" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 to run")
	}

	ctx := context.Background()

	// --- Platform storage (SQLite on disk) ---
	stateDB, err := sql.Open("sqlite", "file:e2e_state.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatalf("open state db: %v", err)
	}
	defer func() { stateDB.Close(); os.Remove("e2e_state.db") }()

	store := sqlstorage.NewSQLStorage(stateDB, "sqlite")
	if err := store.Init(ctx); err != nil {
		t.Fatalf("init store: %v", err)
	}

	// --- Sink SQLite DB ---
	sinkPath := "e2e_sink.db"
	sinkDB, err := sql.Open("sqlite", sinkPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatalf("open sink db: %v", err)
	}
	defer func() { sinkDB.Close(); os.Remove(sinkPath) }()

	table := "e2e_msgs"
	if _, err := sinkDB.Exec("CREATE TABLE IF NOT EXISTS " + table + " (id TEXT PRIMARY KEY, data BLOB NOT NULL)"); err != nil {
		t.Fatalf("create sink table: %v", err)
	}

	// --- Registry ---
	reg := NewRegistry(store)

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
	w1 := NewWorker(store, reg)
	w1.SetWorkerConfig(0, 1, "worker-1", "")
	w1.SetLeaseTTL(3) // seconds
	w1.SetSyncInterval(500 * time.Millisecond)

	w2 := NewWorker(store, reg)
	w2.SetWorkerConfig(0, 1, "worker-2", "")
	w2.SetLeaseTTL(3)
	w2.SetSyncInterval(500 * time.Millisecond)

	// Start worker 1
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	go w1.Start(ctx1)

	// Wait briefly for initial sync
	time.Sleep(500 * time.Millisecond)

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
	if err := webhook.Dispatch("/e2e", m1); err != nil {
		t.Fatalf("dispatch m1: %v", err)
	}
	if err := webhook.Dispatch("/e2e", m2); err != nil {
		t.Fatalf("dispatch m2: %v", err)
	}

	// Wait for processing
	awaitRows(t, sinkDB, table, 2, 5*time.Second)

	// Duplicate id-1 should not create new row (upsert)
	m1d := message.AcquireMessage()
	m1d.SetID("id-1")
	m1d.SetTable(table)
	m1d.SetAfter([]byte(`{"n":1}`))
	defer message.ReleaseMessage(m1d)
	_ = webhook.Dispatch("/e2e", m1d)
	awaitRows(t, sinkDB, table, 2, 2*time.Second)

	// Simulate crash of worker 1
	cancel1()

	// Start worker 2; it should steal the lease after TTL
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go w2.Start(ctx2)

	// Allow TTL to expire and worker 2 to acquire lease
	time.Sleep(4 * time.Second)

	// Ensure the engine is running again under worker 2 before dispatching
	waitDeadline := time.Now().Add(3 * time.Second)
	for {
		if reg.IsEngineRunning(wf.ID) {
			break
		}
		if time.Now().After(waitDeadline) {
			t.Fatalf("engine did not restart on worker 2 in time")
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
	if err := webhook.Dispatch("/e2e", m3); err != nil {
		t.Fatalf("dispatch m3: %v", err)
	}
	if err := webhook.Dispatch("/e2e", m4); err != nil {
		t.Fatalf("dispatch m4: %v", err)
	}

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
