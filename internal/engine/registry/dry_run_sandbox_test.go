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
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	_ "modernc.org/sqlite"

	_ "github.com/gsoultan/hermod/pkg/comm/transformer/advanced"
	_ "github.com/gsoultan/hermod/pkg/comm/transformer/lookup"
)

// ---------------------------------------------------------------------------
// A dry run writes nothing.
//
// Dry run reads live data and runs every node, but writes to no sink and
// acknowledges nothing. Execute SQL and SCD are nodes, not sinks, so a dry run
// used to insert and update real rows through them. Each message now runs in
// its own transaction per database, rolled back when the message's walk ends:
// the nodes after an Execute SQL see its row, and the table never does.
// ---------------------------------------------------------------------------

// sqlPipeStorage is a pipeStorage that also knows real database sources.
type sqlPipeStorage struct {
	*pipeStorage
	sources map[string]storage.Source
}

func (s *sqlPipeStorage) GetSource(ctx context.Context, id string) (storage.Source, error) {
	if src, ok := s.sources[id]; ok {
		return src, nil
	}
	return s.pipeStorage.GetSource(ctx, id)
}

func TestDryRunSeesItsSQLWritesPerMessageAndCommitsNone(t *testing.T) {
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

	// The engine walks several messages at once, and SQLite has one writer:
	// without a busy timeout a second writer fails at once instead of waiting.
	dsn := "file:" + path + "?_pragma=busy_timeout(10000)"
	assertDryRunWritesNothing(t, db, storage.Source{ID: "orders-db", Name: "orders", Type: "sqlite",
		Config: map[string]string{"path": dsn, "use_cdc": "false"}})
}

// assertDryRunWritesNothing dry-runs a workflow that inserts a row per message
// and looks it up on two parallel branches, against db registered as src. It
// expects an empty table named orders (code unique, status).
func assertDryRunWritesNothing(t *testing.T, db *sql.DB, src storage.Source) {
	t.Helper()
	const orders = 4

	store := &sqlPipeStorage{pipeStorage: newPipeStorage(), sources: map[string]storage.Source{src.ID: src}}
	reg := NewRegistry(store)

	lookup := func(target string) map[string]any {
		return map[string]any{
			"transType":     "db_lookup",
			"sourceId":      "orders-db",
			"mode":          "query",
			"queryTemplate": `SELECT status FROM orders WHERE code = {{.order_id}}`,
			"valueColumn":   "status",
			"targetField":   target,
		}
	}
	mark := map[string]any{"transType": "set", "column.marked": "'yes'"}

	// The insert fans out to two lookups on separate branches, which the
	// engine walks on separate goroutines: both use the message's one
	// transaction at once.
	wf := storage.Workflow{
		ID: "wf-dry-sql", Name: "wf-dry-sql", DryRun: true,
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "s-orders"},
			{ID: "ins", Type: "transformation", Config: map[string]any{
				"transType":     "execute_sql",
				"sourceId":      "orders-db",
				"queryTemplate": `INSERT INTO orders (code, status) VALUES ({{.order_id}}, 'new')`,
			}},
			{ID: "look-a", Type: "transformation", Config: lookup("seen_a")},
			{ID: "look-b", Type: "transformation", Config: lookup("seen_b")},
			{ID: "mark-a", Type: "transformation", Config: mark},
			{ID: "mark-b", Type: "transformation", Config: mark},
			{ID: "out-a", Type: "sink", RefID: "snk-a"},
			{ID: "out-b", Type: "sink", RefID: "snk-b"},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e1", SourceID: "src", TargetID: "ins"},
			{ID: "e2", SourceID: "ins", TargetID: "look-a"},
			{ID: "e3", SourceID: "ins", TargetID: "look-b"},
			{ID: "e4", SourceID: "look-a", TargetID: "mark-a"},
			{ID: "e5", SourceID: "look-b", TargetID: "mark-b"},
			{ID: "e6", SourceID: "mark-a", TargetID: "out-a"},
			{ID: "e7", SourceID: "mark-b", TargetID: "out-b"},
		},
		MaxRetries:    1,
		RetryInterval: "10ms",
	}

	live := reg.SubscribeWorkflowLiveMessages(wf.ID)
	logs := reg.SubscribeWorkflowLogs(wf.ID)

	var (
		mu       sync.Mutex
		seen     = map[string]any{} // "<node>/<order>" -> looked-up status
		failures []string
		notes    []string
	)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case m := <-live:
				mu.Lock()
				if m.IsError {
					failures = append(failures, m.NodeID+": "+m.Error)
				}
				switch m.NodeID {
				case "mark-a":
					seen["a/"+asString(m.Data["order_id"])] = m.Data["seen_a"]
				case "mark-b":
					seen["b/"+asString(m.Data["order_id"])] = m.Data["seen_b"]
				}
				mu.Unlock()
			case l := <-logs:
				mu.Lock()
				notes = append(notes, l.Message)
				mu.Unlock()
			case <-done:
				return
			}
		}
	}()
	t.Cleanup(func() { close(done) })

	sinks := map[string]*pipeSink{"snk-a": {name: "a"}, "snk-b": {name: "b"}}
	stop := startForeachPipeline(t, reg, wf, &arraySource{count: orders}, sinks)
	defer stop()

	ok := waitUntil(t, 30*time.Second, "every order through both branches", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(seen) >= 2*orders || len(failures) > 0
	})
	mu.Lock()
	defer mu.Unlock()
	if len(failures) > 0 {
		t.Fatalf("a node failed in the dry run: %v", failures)
	}
	if !ok {
		t.Fatalf("saw %d of %d branch results: %v", len(seen), 2*orders, seen)
	}
	for k, v := range seen {
		if v != "new" {
			t.Errorf("%s: the lookup after the insert saw %#v, want \"new\"", k, v)
		}
	}

	var n int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM orders").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("the dry run wrote %d row(s) to the table; a dry run must write nothing", n)
	}

	found := false
	for _, note := range notes {
		if strings.Contains(note, "[DRY-RUN]") && strings.Contains(note, "rolled back") {
			found = true
		}
	}
	if !found {
		t.Errorf("the dry run's log never says database writes are rolled back per message. Logs: %v", notes)
	}
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}
