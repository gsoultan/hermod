//go:build integration
// +build integration

package registry

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/gsoultan/Hermod/internal/storage"
	sqlstorage "github.com/gsoultan/Hermod/internal/storage/sql"
	"github.com/gsoultan/Hermod/pkg/comm/message"
	"github.com/gsoultan/Hermod/pkg/comm/source/webhook"
	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// Several workflows under sustained load, writing to a real Postgres.
//
// This is what heavy_load_e2e.spec.ts was for. Its browser half — registering
// workers, clicking Start, watching a log panel — is covered by the worker
// hardening tests (TestWorkerStaysOnlineUnderHeavySyncLoad,
// TestWorkerFailoverThenFailbackUnderLoad, TestRendezvousDistributesUnderUnequalLoad)
// and by the required UI job. What none of those cover is the bit that actually
// matters to a user: after a burst of traffic across concurrent workflows, is
// every row in the destination database, exactly once?
//
// Those tests all write to mock sinks. A mock sink cannot tell you that the
// upsert key is right, that concurrent writers do not deadlock, or that a
// retried message overwrites its own row instead of adding one — and those are
// the failures that show up as quiet duplication rather than an error.
// ---------------------------------------------------------------------------

func TestMultiWorkflowLoadDeliversEveryRowExactlyOnce(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and POSTGRES_DSN to run")
	}

	ctx := t.Context()

	sinkDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open sink db: %v", err)
	}
	// t.Cleanup rather than defer: defers run before cleanups, so a deferred
	// Close would shut the handle the DROP below still needs.
	t.Cleanup(func() { _ = sinkDB.Close() })
	if err := sinkDB.PingContext(ctx); err != nil {
		t.Fatalf("ping sink db: %v", err)
	}

	const (
		workflows       = 3
		perWorkflow     = 150
		duplicateEveryN = 10 // redeliveries, which the sink must collapse
	)

	table := "load_" + strings.ToLower(t.Name())
	mustExecDB(t, sinkDB, "DROP TABLE IF EXISTS "+table)
	mustExecDB(t, sinkDB, fmt.Sprintf(
		`CREATE TABLE %s (id TEXT PRIMARY KEY, data JSONB)`, table))
	t.Cleanup(func() { mustExecDB(t, sinkDB, "DROP TABLE IF EXISTS "+table) })

	// Metadata in SQLite: this test is about the data plane, and a second
	// Postgres database would only add connections to the same server.
	meta, err := sql.Open("sqlite", "file:mwload?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open metadata db: %v", err)
	}
	t.Cleanup(func() { _ = meta.Close() })
	store := sqlstorage.NewSQLStorage(meta, "sqlite")
	if err := store.Init(ctx); err != nil {
		t.Fatalf("init metadata: %v", err)
	}

	reg := NewRegistry(store)

	type wf struct {
		id   string
		path string
	}
	var built []wf

	for i := range workflows {
		srcID := fmt.Sprintf("mw-src-%d", i)
		snkID := fmt.Sprintf("mw-snk-%d", i)
		wfID := fmt.Sprintf("mw-wf-%d", i)
		path := fmt.Sprintf("/mw-%d", i)

		if err := store.CreateSource(ctx, storage.Source{
			ID: srcID, Name: srcID, Type: "webhook", Active: true,
			Config: map[string]string{"path": path},
		}); err != nil {
			t.Fatalf("create source %d: %v", i, err)
		}
		if err := store.CreateSink(ctx, storage.Sink{
			ID: snkID, Name: snkID, Type: "postgres", Active: true,
			Config: map[string]string{
				"connection_string":  dsn,
				"table":              table,
				"use_existing_table": "true",
			},
		}); err != nil {
			t.Fatalf("create sink %d: %v", i, err)
		}
		if err := store.CreateWorkflow(ctx, storage.Workflow{
			ID: wfID, Name: wfID, Active: true,
			Nodes: []storage.WorkflowNode{
				{ID: "s", Type: "source", RefID: srcID},
				{ID: "k", Type: "sink", RefID: snkID},
			},
			Edges: []storage.WorkflowEdge{{ID: "e", SourceID: "s", TargetID: "k"}},
		}); err != nil {
			t.Fatalf("create workflow %d: %v", i, err)
		}

		w, err := store.GetWorkflow(ctx, wfID)
		if err != nil {
			t.Fatalf("get workflow %d: %v", i, err)
		}
		if err := reg.StartWorkflow(wfID, w); err != nil {
			t.Fatalf("start workflow %d: %v", i, err)
		}
		t.Cleanup(func() { _ = reg.StopEngine(ctx, wfID) })

		built = append(built, wf{id: wfID, path: path})
	}

	// Every workflow takes traffic at once, which is the part a single-engine
	// test cannot exercise: concurrent writers against one destination table.
	var wg sync.WaitGroup
	for _, w := range built {
		wg.Add(1)
		go func(w wf) {
			defer wg.Done()
			for n := range perWorkflow {
				id := fmt.Sprintf("%s-%d", w.id, n)
				dispatchUntilAccepted(t, w.path, table, id)
				// Redeliver periodically. Delivery is at-least-once, so this is
				// normal traffic, and the sink's upsert is what makes it
				// exactly-once at the destination.
				if n%duplicateEveryN == 0 {
					dispatchUntilAccepted(t, w.path, table, id)
				}
			}
		}(w)
	}
	wg.Wait()

	want := workflows * perWorkflow
	deadline := time.Now().Add(90 * time.Second)
	var got int
	for time.Now().Before(deadline) {
		if err := sinkDB.QueryRowContext(ctx,
			"SELECT count(*) FROM "+table).Scan(&got); err != nil {
			t.Fatalf("counting rows: %v", err)
		}
		if got >= want {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}

	if got < want {
		t.Errorf("%d of %d rows reached Postgres after 90s across %d concurrent workflows; "+
			"messages acknowledged to their sources are missing from the destination",
			got, want, workflows)
	}
	if got > want {
		t.Errorf("%d rows in Postgres but only %d distinct messages were sent; the "+
			"redeliveries landed as new rows instead of overwriting their own, so the "+
			"sink upsert is not keyed on the message identity", got, want)
	}
}

// dispatchUntilAccepted pushes one message, waiting out the two conditions a
// producer legitimately meets.
//
// "no webhook registered" means the workflow has not finished starting. "buffer
// full" is backpressure: the webhook source bounds its queue and refuses rather
// than dropping, which is the correct behaviour and the whole point of pushing
// hard enough to hit it. Treating either as fatal — as this first did — turns a
// healthy pipeline into a failed test.
func dispatchUntilAccepted(t *testing.T, path, table, id string) {
	t.Helper()
	m := message.AcquireMessage()
	m.SetID(id)
	// SetAfter, not SetPayload: the SQL sinks write the after-image into the
	// data column, so a payload-only message lands as invalid JSON. SetTable is
	// what routes it, since the sink takes the table from the message when its
	// own config does not pin one.
	m.SetTable(table)
	m.SetAfter([]byte(fmt.Sprintf(`{"id":%q}`, id)))
	// No Release here. Dispatch hands the message to the source's channel and
	// ownership goes with it; the pipeline releases it once the sink has
	// written it. Releasing here returns it to the pool while the engine still
	// holds it, and the sink then reads a message that has been reset and
	// refilled -- which showed up as an empty id and invalid JSON rather than
	// as anything resembling a use-after-free.

	deadline := time.Now().Add(60 * time.Second)
	for {
		err := webhook.Dispatch(path, m)
		if err == nil {
			return
		}
		msg := err.Error()
		retryable := strings.Contains(msg, "no webhook registered") || strings.Contains(msg, "buffer full")
		if !retryable || time.Now().After(deadline) {
			t.Fatalf("dispatch to %s: %v", path, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// mustExecDB uses a background context deliberately: it also runs from
// t.Cleanup, by which point t.Context() has been cancelled and the DROP would
// fail, leaving the table behind for the next run.
func mustExecDB(t *testing.T, db *sql.DB, q string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), q); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}
