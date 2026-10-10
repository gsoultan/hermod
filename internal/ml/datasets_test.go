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
