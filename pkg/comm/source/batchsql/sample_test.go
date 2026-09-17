package batchsql

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// A batch_sql source is query-driven: it holds no table of its own, only a cron
// and a list of whole SQL statements run against a delegate database. Every
// other source the editor samples names a table, so the sampling path handed
// Sample a table name and Sample built "SELECT * FROM <table> LIMIT 1" from it.
// For batch_sql there is no such name — config carries `queries`, never `table`
// or `tables` — so the editor sent the empty string and the source ran
// "SELECT * FROM  LIMIT 1".
//
// The row that came back (none) is what the workflow editor uses to populate
// Available Fields for every downstream node, so a db_lookup wired to a
// batch_sql source showed an empty field list with nothing reporting an error.
func TestSampleWithoutTableUsesConfiguredQuery(t *testing.T) {
	db := openSampleDB(t)

	source := NewBatchSQLSource(&mockDBProvider{db: db}, Config{
		SourceID: "test-source",
		Cron:     "0 0 * * *",
		Queries:  `["SELECT id, name, region FROM orders ORDER BY id"]`,
	})
	defer source.Close()

	msg, err := source.Sample(context.Background(), "")
	if err != nil {
		t.Fatalf("Sample with no table name failed: %v", err)
	}
	if msg == nil {
		t.Fatal("Sample returned no message; the editor has no fields to offer downstream nodes")
	}

	data := msg.Data()
	for _, col := range []string{"id", "name", "region"} {
		if _, ok := data[col]; !ok {
			t.Errorf("sample is missing column %q that the configured query selects; got %v", col, data)
		}
	}
}

// The watermark template is substituted before a scheduled run, so a query
// carrying {{.last_value}} is not valid SQL until it is. Sampling has no prior
// run to draw a watermark from, and leaving the token in place makes the
// preview fail on exactly the queries the incremental mode exists for.
func TestSampleSubstitutesLastValueTemplate(t *testing.T) {
	db := openSampleDB(t)

	source := NewBatchSQLSource(&mockDBProvider{db: db}, Config{
		SourceID:          "test-source",
		Cron:              "0 0 * * *",
		Queries:           `["SELECT id, name FROM orders WHERE id > '{{.last_value}}' OR '{{.last_value}}' = '' ORDER BY id"]`,
		IncrementalColumn: "id",
	})
	defer source.Close()

	msg, err := source.Sample(context.Background(), "")
	if err != nil {
		t.Fatalf("Sample on a templated query failed: %v", err)
	}
	if msg == nil {
		t.Fatal("Sample returned no message for a templated query")
	}
	if _, ok := msg.Data()["name"]; !ok {
		t.Errorf("sample is missing the columns the templated query selects; got %v", msg.Data())
	}
}

// A table name is still honoured when the caller supplies one: the sink-side
// preview and the column browser both pass a real table, and that path must
// keep working.
func TestSampleStillHonoursAnExplicitTable(t *testing.T) {
	db := openSampleDB(t)

	source := NewBatchSQLSource(&mockDBProvider{db: db}, Config{
		SourceID: "test-source",
		Queries:  `["SELECT id FROM orders"]`,
	})
	defer source.Close()

	msg, err := source.Sample(context.Background(), "orders")
	if err != nil {
		t.Fatalf("Sample on an explicit table failed: %v", err)
	}
	if _, ok := msg.Data()["region"]; !ok {
		t.Errorf("an explicit table sample should return every column; got %v", msg.Data())
	}
}

func openSampleDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	if _, err := db.ExecContext(context.Background(), "CREATE TABLE orders (id INTEGER PRIMARY KEY, name TEXT, region TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), "INSERT INTO orders (name, region) VALUES ('first', 'eu'), ('second', 'us')"); err != nil {
		t.Fatal(err)
	}
	return db
}
