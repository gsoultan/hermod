// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package ml

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gsoultan/hermod/pkg/ml/worker"

	_ "modernc.org/sqlite"
)

// rowSink is a worker's dataset append route that keeps every call.
type rowSink struct {
	mu    sync.Mutex
	calls []struct {
		Rows    []map[string]any `json:"rows"`
		Replace bool             `json:"replace"`
	}
	path  string
	total int
}

func (s *rowSink) worker(t *testing.T) *worker.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.path = r.URL.Path
		var c struct {
			Rows    []map[string]any `json:"rows"`
			Replace bool             `json:"replace"`
		}
		_ = json.NewDecoder(r.Body).Decode(&c)
		s.calls = append(s.calls, c)
		if c.Replace {
			s.total = 0
		}
		s.total += len(c.Rows)
		_ = json.NewEncoder(w).Encode(map[string]any{"rows": s.total})
	}))
	t.Cleanup(srv.Close)
	return worker.New(srv.URL, "", nil)
}

func ordersDB(t *testing.T, n int) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:mlq_"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE orders (id INTEGER, amount REAL, country TEXT, note BLOB)`); err != nil {
		t.Fatal(err)
	}
	for i := range n {
		if _, err := db.ExecContext(t.Context(), `INSERT INTO orders VALUES (?, ?, ?, ?)`, i, float64(i)*1.5, "ID", []byte("b")); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestDatasetFromQueryStreamsRowsInChunksAndReplacesFirst(t *testing.T) {
	sink := &rowSink{}
	svc := NewService(func() any { return newMemStore() }, nil, nil).WithWorker(sink.worker(t))
	db := ordersDB(t, queryChunkRows+5)

	n, err := svc.DatasetFromQuery(t.Context(), "tenant-a", "orders", db, "SELECT id, amount, country, note FROM orders", 0)
	if err != nil {
		t.Fatalf("DatasetFromQuery: %v", err)
	}
	if n != queryChunkRows+5 {
		t.Errorf("rows = %d, want %d", n, queryChunkRows+5)
	}
	if len(sink.calls) != 2 || !sink.calls[0].Replace || sink.calls[1].Replace {
		t.Fatalf("calls = %d (replace %v), want a replacing chunk then an appending one", len(sink.calls), sink.calls[0].Replace)
	}
	if sink.path != "/v1/datasets/tenant-a/orders/rows" {
		t.Errorf("path = %s", sink.path)
	}
	first := sink.calls[0].Rows[1]
	if first["country"] != "ID" || first["amount"] != 1.5 || first["note"] != "b" {
		t.Errorf("row = %v, want text for a BLOB and the values as stored", first)
	}
}

func TestDatasetFromQueryStopsAtTheRowCap(t *testing.T) {
	sink := &rowSink{}
	svc := NewService(func() any { return newMemStore() }, nil, nil).WithWorker(sink.worker(t))
	db := ordersDB(t, 30)
	_, err := svc.DatasetFromQuery(t.Context(), "v", "orders", db, "SELECT * FROM orders", 10)
	if err == nil || !strings.Contains(err.Error(), "10") {
		t.Errorf("err = %v, want a refusal naming the cap", err)
	}
}

func TestDatasetFromQueryReadsOnly(t *testing.T) {
	sink := &rowSink{}
	svc := NewService(func() any { return newMemStore() }, nil, nil).WithWorker(sink.worker(t))
	db := ordersDB(t, 1)
	for _, q := range []string{"DELETE FROM orders", "  update orders set id = 1", "DROP TABLE orders", ""} {
		if _, err := svc.DatasetFromQuery(t.Context(), "v", "orders", db, q, 0); err == nil {
			t.Errorf("%q was run", q)
		}
	}
	var n int
	_ = db.QueryRowContext(t.Context(), `SELECT count(*) FROM orders`).Scan(&n)
	if n != 1 || len(sink.calls) != 0 {
		t.Errorf("rows left = %d, worker calls = %d", n, len(sink.calls))
	}
	if _, err := svc.DatasetFromQuery(t.Context(), "v", "orders", db, "  with x as (select 1 as a) select a from x", 0); err != nil {
		t.Errorf("a WITH query was refused: %v", err)
	}
}

func TestPreviewQueryReturnsTheFirstRowsAndSaysThereAreMore(t *testing.T) {
	db := ordersDB(t, 30)
	p, err := PreviewQuery(t.Context(), db, "SELECT id, country, note FROM orders ORDER BY id", 5)
	if err != nil {
		t.Fatalf("PreviewQuery: %v", err)
	}
	if strings.Join(p.Columns, ",") != "id,country,note" {
		t.Errorf("columns = %v, want the query's own order", p.Columns)
	}
	if len(p.Rows) != 5 || !p.More {
		t.Fatalf("rows = %d, more = %v; want 5 and more", len(p.Rows), p.More)
	}
	if p.Rows[4]["id"] != int64(4) || p.Rows[0]["note"] != "b" {
		t.Errorf("rows = %v, want the values as the dataset would get them", p.Rows)
	}

	all, err := PreviewQuery(t.Context(), db, "SELECT id FROM orders WHERE id < 3", 5)
	if err != nil || len(all.Rows) != 3 || all.More {
		t.Errorf("a short result: rows = %d, more = %v, err = %v", len(all.Rows), all.More, err)
	}
	none, err := PreviewQuery(t.Context(), db, "SELECT id FROM orders WHERE id < 0", 5)
	if err != nil || none.Rows == nil || len(none.Columns) != 1 {
		t.Errorf("no rows: %+v, %v; want the columns and an empty list", none, err)
	}
}

func TestPreviewQueryReadsOnlyAndCapsTheRows(t *testing.T) {
	db := ordersDB(t, 300)
	if _, err := PreviewQuery(t.Context(), db, "DELETE FROM orders", 5); err == nil {
		t.Error("a DELETE was previewed")
	}
	var n int
	_ = db.QueryRowContext(t.Context(), `SELECT count(*) FROM orders`).Scan(&n)
	if n != 300 {
		t.Errorf("rows left = %d", n)
	}
	p, err := PreviewQuery(t.Context(), db, "SELECT id FROM orders", 0)
	if err != nil || len(p.Rows) != MaxPreviewRows {
		t.Errorf("no limit: rows = %d, err = %v; want %d", len(p.Rows), err, MaxPreviewRows)
	}
	p, _ = PreviewQuery(t.Context(), db, "SELECT id FROM orders", 10_000)
	if len(p.Rows) != MaxPreviewRows {
		t.Errorf("a limit over the cap: rows = %d, want %d", len(p.Rows), MaxPreviewRows)
	}
}
