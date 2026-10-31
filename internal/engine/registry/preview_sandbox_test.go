// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package registry

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	sqlstorage "github.com/gsoultan/hermod/internal/storage/sql"
	"github.com/gsoultan/hermod/pkg/comm/message"
	_ "modernc.org/sqlite"

	_ "github.com/gsoultan/hermod/pkg/comm/transformer/advanced"
	_ "github.com/gsoultan/hermod/pkg/comm/transformer/lookup"
)

// ---------------------------------------------------------------------------
// Previews do not write.
//
// Simulate and Run Preview used to run an Execute SQL node's INSERT or UPDATE
// for real: pressing a button in the editor changed production tables. They
// now run every SQL node of the run in one transaction per database that is
// rolled back at the end. Nodes further down still see the rows -- a DB Lookup
// after the insert finds it, a second Execute SQL can update it -- because
// they share that transaction.
// ---------------------------------------------------------------------------

// newSandboxRegistry is a registry whose store holds a sample source and a
// SQLite database registered as a source, holding one empty table.
func newSandboxRegistry(t *testing.T) (*Registry, *sql.DB) {
	t.Helper()
	meta, err := sql.Open("sqlite", "file:sandbox_"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open metadata db: %v", err)
	}
	t.Cleanup(func() { _ = meta.Close() })
	store := sqlstorage.NewSQLStorage(meta, "sqlite")
	if err := store.Init(t.Context()); err != nil {
		t.Fatalf("init store: %v", err)
	}

	path := filepath.Join(t.TempDir(), "orders.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(t.Context(),
		`CREATE TABLE orders (id INTEGER PRIMARY KEY, code TEXT UNIQUE, status TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}

	for _, src := range []storage.Source{
		{ID: "src-1", Name: "sample", Type: "webhook", Config: map[string]string{"path": "/sandbox"}},
		{ID: "orders-db", Name: "orders", Type: "sqlite", Config: map[string]string{"path": path, "use_cdc": "false"}},
	} {
		if err := store.CreateSource(t.Context(), src); err != nil {
			t.Fatalf("create source %s: %v", src.ID, err)
		}
	}
	return NewRegistry(store), db
}

// insertLookupUpdateLookup is the chain from the report: write a row, read it
// back, change it, read it again. Both lookups are the same query on the same
// message, so the second one is also the lookup cache's chance to answer with
// the first one's stale value.
func insertLookupUpdateLookup() []storage.Transformation {
	return []storage.Transformation{
		{Type: "execute_sql", Config: map[string]any{
			"sourceId":      "orders-db",
			"queryTemplate": `INSERT INTO orders (code, status) VALUES ({{.code}}, 'new') RETURNING id, code, status`,
			"resultField":   "inserted",
		}},
		{Type: "db_lookup", Config: map[string]any{
			"sourceId":      "orders-db",
			"mode":          "query",
			"queryTemplate": `SELECT status FROM orders WHERE code = {{.code}}`,
			"valueColumn":   "status",
			"targetField":   "status_after_insert",
		}},
		{Type: "execute_sql", Config: map[string]any{
			"sourceId":          "orders-db",
			"queryTemplate":     `UPDATE orders SET status = 'paid' WHERE code = {{.code}}`,
			"affectedRowsField": "updated",
		}},
		{Type: "db_lookup", Config: map[string]any{
			"sourceId":      "orders-db",
			"mode":          "query",
			"queryTemplate": `SELECT status FROM orders WHERE code = {{.code}}`,
			"valueColumn":   "status",
			"targetField":   "status_after_update",
		}},
	}
}

func assertTableUntouched(t *testing.T, db *sql.DB) {
	t.Helper()
	var n int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM orders").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("the preview wrote %d row(s) to the table; a preview must write nothing", n)
	}
}

func TestSimulationSeesItsSQLWritesAndCommitsNone(t *testing.T) {
	reg, db := newSandboxRegistry(t)

	trans := insertLookupUpdateLookup()
	wf := storage.Workflow{ID: "sandbox-wf", Nodes: []storage.WorkflowNode{{ID: "src", Type: "source", RefID: "src-1"}}}
	prev := "src"
	for i, tr := range trans {
		cfg := map[string]any{"transType": tr.Type}
		for k, v := range tr.Config {
			cfg[k] = v
		}
		id := fmt.Sprintf("t%d", i+1)
		wf.Nodes = append(wf.Nodes, storage.WorkflowNode{ID: id, Type: "transformation", Config: cfg})
		wf.Edges = append(wf.Edges, storage.WorkflowEdge{ID: "e" + id, SourceID: prev, TargetID: id})
		prev = id
	}

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetAfter([]byte(`{"code":"C-1"}`))

	steps, err := reg.SimulateWorkflow(t.Context(), wf, SimulationInput{Message: msg, Partial: true})
	if err != nil {
		t.Fatalf("SimulateWorkflow: %v", err)
	}
	last := steps[len(steps)-1]
	for _, s := range steps {
		if s.Error != "" {
			t.Fatalf("step %s failed: %s", s.NodeID, s.Error)
		}
	}
	if last.NodeID != "t4" {
		t.Fatalf("the run stopped at %s, want t4. Steps: %+v", last.NodeID, steps)
	}
	assertSandboxPayload(t, last.Payload)
	assertTableUntouched(t, db)

	// A preview must not leave the live engine a cached row that was never
	// committed. A live lookup of C-1 asks the database, not the preview.
	if n, _ := reg.GetLookupCacheSize(); n != 0 {
		t.Errorf("the preview left %d entr(ies) in the live lookup cache", n)
	}
}

func TestRunPreviewSeesItsSQLWritesAndCommitsNone(t *testing.T) {
	reg, db := newSandboxRegistry(t)

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetAfter([]byte(`{"code":"C-1"}`))
	msg.SetPayload([]byte(`{"code":"C-1"}`))

	results, err := reg.TestTransformationPipeline(t.Context(), insertLookupUpdateLookup(), msg)
	if err != nil {
		t.Fatalf("TestTransformationPipeline: %v", err)
	}
	t.Cleanup(func() {
		for _, r := range results {
			if r != nil {
				r.Release()
			}
		}
	})
	last := results[len(results)-1]
	if last == nil {
		t.Fatal("the last step emitted nothing")
	}
	assertSandboxPayload(t, last.ToMap())
	assertTableUntouched(t, db)
}

// Two previews of the same chain in a row must both succeed. The first one
// leaving its row behind is exactly what used to fail the second on the
// UNIQUE code.
func TestRunPreviewCanBeRepeated(t *testing.T) {
	reg, db := newSandboxRegistry(t)
	for i := range 2 {
		msg := message.AcquireMessage()
		msg.SetAfter([]byte(`{"code":"C-1"}`))
		msg.SetPayload([]byte(`{"code":"C-1"}`))
		results, err := reg.TestTransformationPipeline(t.Context(), insertLookupUpdateLookup(), msg)
		msg.Release()
		if err != nil {
			t.Fatalf("preview %d: %v", i+1, err)
		}
		for _, r := range results {
			if r != nil {
				r.Release()
			}
		}
	}
	assertTableUntouched(t, db)
}

func assertSandboxPayload(t *testing.T, payload map[string]any) {
	t.Helper()
	inserted, ok := payload["inserted"].(map[string]any)
	if !ok {
		t.Fatalf("inserted = %#v, want the RETURNING row. Payload: %v", payload["inserted"], payload)
	}
	if inserted["code"] != "C-1" || inserted["status"] != "new" {
		t.Errorf("inserted = %v, want code C-1 status new", inserted)
	}
	if payload["status_after_insert"] != "new" {
		t.Errorf("the lookup after the insert saw %#v, want \"new\": a later node must see the run's write",
			payload["status_after_insert"])
	}
	if fmt.Sprint(payload["updated"]) != "1" {
		t.Errorf("updated = %#v, want 1: the update must find the row the run inserted", payload["updated"])
	}
	if payload["status_after_update"] != "paid" {
		t.Errorf("the lookup after the update saw %#v, want \"paid\"", payload["status_after_update"])
	}
}
