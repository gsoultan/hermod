package sqlutil

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sync/atomic"
	"testing"

	_ "modernc.org/sqlite"
)

// ScanRows is the generic database/sql read path: db_lookup uses it for both
// key-column and query-template mode, and the editor's SQL builder falls back
// to it whenever a query binds arguments. It used to render every []byte as a
// string, so a PostgreSQL `jsonb` column reached transformations, sinks and the
// message trace as a string that happened to contain JSON.
//
// That disagreed with every other way the same column can enter a pipeline --
// PostgresSource.ExecuteSQL and the source's own snapshot/polling paths go
// through pgx natively and produce a map -- so the same document had two shapes
// depending on which path fetched it, and no field picker could see inside the
// string half.

// jsonDriverRows reproduces what pgx's database/sql driver actually hands over:
// the raw document as []byte, with the column type named "JSONB". Measured
// against pgx v5.9.2 and PostgreSQL 18 -- stdlib/sql.go routes JSONOID/JSONBOID
// through a []byte scan plan, and ColumnTypeDatabaseTypeName uppercases the
// pgtype name.
//
// A fake is the only way to pin that contract in the default suite. No in-repo
// driver both names a column JSONB *and* delivers []byte: sqlite reports the
// declared type but hands text over as a string, which is why
// TestScanRowsDecodesADeclaredJSONColumnOnSQLite exists alongside this.
type jsonDriverRows struct {
	cols  []string
	types []string
	vals  [][]driver.Value
	pos   int
}

func (r *jsonDriverRows) Columns() []string { return r.cols }
func (r *jsonDriverRows) Close() error      { return nil }

func (r *jsonDriverRows) Next(dest []driver.Value) error {
	if r.pos >= len(r.vals) {
		return io.EOF
	}
	copy(dest, r.vals[r.pos])
	r.pos++
	return nil
}

func (r *jsonDriverRows) ColumnTypeDatabaseTypeName(i int) string { return r.types[i] }

// untypedDriverRows is the same result set from a driver that will not say what
// its columns are. ScanRows must treat that as "no JSON columns" -- a driver
// withholding type metadata must not change a pipeline's shape.
//
// Written out rather than embedding jsonDriverRows: an embedded method still
// satisfies driver.RowsColumnTypeDatabaseTypeName, so database/sql would call
// it and this would test the opposite of what it claims to.
type untypedDriverRows struct {
	cols []string
	vals [][]driver.Value
	pos  int
}

func (r *untypedDriverRows) Columns() []string { return r.cols }
func (r *untypedDriverRows) Close() error      { return nil }

func (r *untypedDriverRows) Next(dest []driver.Value) error {
	if r.pos >= len(r.vals) {
		return io.EOF
	}
	copy(dest, r.vals[r.pos])
	r.pos++
	return nil
}

// failingDriverRows stops part-way through with a real error, the way a
// connection dropped mid-result-set does.
type failingDriverRows struct {
	cols []string
	vals [][]driver.Value
	pos  int
	err  error
}

func (r *failingDriverRows) Columns() []string { return r.cols }
func (r *failingDriverRows) Close() error      { return nil }

func (r *failingDriverRows) Next(dest []driver.Value) error {
	if r.pos >= len(r.vals) {
		return r.err
	}
	copy(dest, r.vals[r.pos])
	r.pos++
	return nil
}

type fakeRowsDriver struct {
	newRows func() driver.Rows
}

func (d *fakeRowsDriver) Open(string) (driver.Conn, error) { return &fakeRowsConn{d}, nil }

type fakeRowsConn struct{ d *fakeRowsDriver }

func (c *fakeRowsConn) Prepare(string) (driver.Stmt, error) { return &fakeRowsStmt{c.d}, nil }
func (c *fakeRowsConn) Close() error                        { return nil }
func (c *fakeRowsConn) Begin() (driver.Tx, error)           { return nil, io.EOF }

type fakeRowsStmt struct{ d *fakeRowsDriver }

func (s *fakeRowsStmt) Close() error                               { return nil }
func (s *fakeRowsStmt) NumInput() int                              { return 0 }
func (s *fakeRowsStmt) Exec([]driver.Value) (driver.Result, error) { return nil, io.EOF }
func (s *fakeRowsStmt) Query([]driver.Value) (driver.Rows, error)  { return s.d.newRows(), nil }

var fakeDriverSeq atomic.Int64

// openFakeRows registers a one-shot driver and returns the *sql.Rows ScanRows
// will be handed. Each call gets its own driver name because sql.Register
// panics on a duplicate and cannot unregister.
func openFakeRows(t *testing.T, newRows func() driver.Rows) *sql.Rows {
	t.Helper()
	name := fmt.Sprintf("hermod-fake-rows-%d", fakeDriverSeq.Add(1))
	sql.Register(name, &fakeRowsDriver{newRows: newRows})
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatalf("open fake driver: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	rows, err := db.QueryContext(t.Context(), "SELECT 1")
	if err != nil {
		t.Fatalf("query fake driver: %v", err)
	}
	t.Cleanup(func() { _ = rows.Close() })
	return rows
}

func TestScanRowsDecodesJSONColumns(t *testing.T) {
	rows := openFakeRows(t, func() driver.Rows {
		return &jsonDriverRows{
			cols:  []string{"id", "email", "meta", "tags"},
			types: []string{"INT4", "TEXT", "JSONB", "JSON"},
			vals: [][]driver.Value{{
				int64(1),
				[]byte("ada@example.com"),
				[]byte(`{"vip": true, "addr": {"city": "London"}}`),
				[]byte(`[1, 2, 3]`),
			}},
		}
	})

	out, err := ScanRows(rows)
	if err != nil {
		t.Fatalf("ScanRows: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d rows, want 1", len(out))
	}
	row := out[0]

	meta, ok := row["meta"].(map[string]any)
	if !ok {
		t.Fatalf("meta = %#v (%T), want a map -- a jsonb column that arrives as a string "+
			"has no fields for a field picker, a sink mapping or a trace to reach into", row["meta"], row["meta"])
	}
	addr, ok := meta["addr"].(map[string]any)
	if !ok {
		t.Fatalf("meta.addr = %#v, want a nested map", meta["addr"])
	}
	if addr["city"] != "London" {
		t.Errorf("meta.addr.city = %#v, want %q", addr["city"], "London")
	}

	tags, ok := row["tags"].([]any)
	if !ok {
		t.Fatalf("tags = %#v (%T), want a slice", row["tags"], row["tags"])
	}
	if len(tags) != 3 {
		t.Errorf("tags = %#v, want 3 elements", tags)
	}

	// Everything else keeps the shape it has always had. Handing every column
	// to a decoder would turn `id` from "7" into 7 across every existing
	// workflow, which is a far larger change than the one being made.
	if row["email"] != "ada@example.com" {
		t.Errorf("email = %#v, want the plain string", row["email"])
	}
	if row["id"] != int64(1) {
		t.Errorf("id = %#v (%T), want int64(1)", row["id"], row["id"])
	}
}

func TestScanRowsLeavesATextColumnHoldingJSONAlone(t *testing.T) {
	rows := openFakeRows(t, func() driver.Rows {
		return &jsonDriverRows{
			cols:  []string{"note", "blob"},
			types: []string{"TEXT", "BLOB"},
			vals: [][]driver.Value{{
				[]byte(`{"looks":"like json"}`),
				[]byte(`[1,2,3]`),
			}},
		}
	})

	out, err := ScanRows(rows)
	if err != nil {
		t.Fatalf("ScanRows: %v", err)
	}
	if got := out[0]["note"]; got != `{"looks":"like json"}` {
		t.Errorf("note = %#v, want the string unchanged -- a text column holding JSON is "+
			"still a text column, and deciding from content would reshape every string that parses", got)
	}
	if got := out[0]["blob"]; got != `[1,2,3]` {
		t.Errorf("blob = %#v, want the string unchanged", got)
	}
}

func TestScanRowsIsUnchangedWhenTheDriverWillNotNameItsColumns(t *testing.T) {
	rows := openFakeRows(t, func() driver.Rows {
		return &untypedDriverRows{
			cols: []string{"meta"},
			vals: [][]driver.Value{{[]byte(`{"a":1}`)}},
		}
	})

	out, err := ScanRows(rows)
	if err != nil {
		t.Fatalf("ScanRows: %v", err)
	}
	if got := out[0]["meta"]; got != `{"a":1}` {
		t.Errorf("meta = %#v, want the string unchanged", got)
	}
}

// A result set that ends in an error is not a shorter result set. Without this
// the caller -- a db_lookup, or the editor's SQL builder -- cannot tell "the
// table has two matching rows" from "the connection dropped after two rows",
// and a lookup silently returns the wrong answer instead of failing.
func TestScanRowsReportsAMidStreamFailureRatherThanTruncating(t *testing.T) {
	boom := errors.New("connection reset by peer")
	rows := openFakeRows(t, func() driver.Rows {
		return &failingDriverRows{
			cols: []string{"id"},
			vals: [][]driver.Value{{int64(1)}, {int64(2)}},
			err:  boom,
		}
	})

	out, err := ScanRows(rows)
	if !errors.Is(err, boom) {
		t.Fatalf("ScanRows returned (%d rows, %v), want the driver's error", len(out), err)
	}
	if out != nil {
		t.Errorf("ScanRows returned %d rows alongside the error, want none", len(out))
	}
}

// SQLite has no JSON type, but it stores the declared type verbatim and
// modernc.org/sqlite reports it, so `meta JSON` is named "JSON" while a TEXT
// column holding the same bytes is named "TEXT". Decoding the first and not the
// second is the whole of IsJSONColumnType's contract; this pins that the
// consequence for SQLite is deliberate rather than accidental.
func TestScanRowsDecodesADeclaredJSONColumnOnSQLite(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	if _, err := db.ExecContext(t.Context(), `CREATE TABLE t (id INTEGER, meta JSON, note TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO t VALUES (1, '{"a":1}', '{"a":1}')`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	rows, err := db.QueryContext(t.Context(), `SELECT id, meta, note FROM t`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	out, err := ScanRows(rows)
	if err != nil {
		t.Fatalf("ScanRows: %v", err)
	}
	if got, want := out[0]["meta"], map[string]any{"a": float64(1)}; !reflect.DeepEqual(got, want) {
		t.Errorf("meta = %#v, want %#v -- the column was declared JSON", got, want)
	}
	if got := out[0]["note"]; got != `{"a":1}` {
		t.Errorf("note = %#v, want the string unchanged -- the column was declared TEXT", got)
	}
}
