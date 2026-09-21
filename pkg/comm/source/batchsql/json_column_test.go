package batchsql

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/gsoultan/hermod/pkg/engine/telemetry"

	_ "modernc.org/sqlite"
)

// batch_sql is the one database source that does not read through its own
// driver's codecs: it borrows a *sql.DB from whatever source it delegates to
// and scans generically. So a `jsonb` column on a batch_sql over PostgreSQL
// arrived as a string holding JSON, exactly as it did through db_lookup before
// sqlutil.ScanRows learned to decode -- and for the same reason, because both
// read through database/sql, where pgx hands JSON columns over as raw []byte.
//
// batch_sql has two such loops and they fail differently:
//
//   - Sample feeds the workflow editor's Available Fields for every downstream
//     node, so the document offered no sub-paths to pick from;
//   - the run loop feeds the pipeline, so a sink mapping or a field path into
//     the document resolved to nothing at runtime.
//
// sqlite stands in for PostgreSQL here because it reports a column's *declared*
// type, so `meta JSON` is named "JSON" and a TEXT column holding identical
// bytes is not. That is the same signal IsJSONColumnType reads from pgx, and it
// lets the narrowness be tested in the default suite rather than only against a
// live server.
func openJSONColumnDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.ExecContext(t.Context(),
		`CREATE TABLE orders (id INTEGER PRIMARY KEY, meta JSON, note TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(),
		`INSERT INTO orders (id, meta, note) VALUES (1, '{"addr":{"city":"London"},"vip":true}', '{"still":"text"}')`); err != nil {
		t.Fatal(err)
	}
	return db
}

func assertDecodedOrderRow(t *testing.T, data map[string]any) {
	t.Helper()

	meta, ok := data["meta"].(map[string]any)
	if !ok {
		t.Fatalf("meta = %#v (%T), want a map -- a JSON column that arrives as a string "+
			"offers no fields to a field picker, a sink mapping or a trace", data["meta"], data["meta"])
	}
	addr, ok := meta["addr"].(map[string]any)
	if !ok {
		t.Fatalf("meta.addr = %#v, want a nested map", meta["addr"])
	}
	if addr["city"] != "London" {
		t.Errorf("meta.addr.city = %#v, want %q", addr["city"], "London")
	}
	if meta["vip"] != true {
		t.Errorf("meta.vip = %#v (%T), want the boolean true", meta["vip"], meta["vip"])
	}

	// The narrowness is the point: a text column holding a JSON document is
	// still text, because deciding from content would reshape every string
	// that happens to parse.
	if got := data["note"]; got != `{"still":"text"}` {
		t.Errorf("note = %#v, want the string unchanged -- the column was declared TEXT", got)
	}
}

func TestSampleDecodesAJSONColumn(t *testing.T) {
	source := NewBatchSQLSource(&mockDBProvider{db: openJSONColumnDB(t)}, Config{
		SourceID: "test-source",
		Cron:     "0 0 * * *",
		Queries:  `["SELECT id, meta, note FROM orders ORDER BY id"]`,
	})
	defer source.Close()

	msg, err := source.Sample(context.Background(), "")
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	if msg == nil {
		t.Fatal("Sample returned no message")
	}
	assertDecodedOrderRow(t, msg.Data())
}

func TestReadDecodesAJSONColumn(t *testing.T) {
	source := NewBatchSQLSource(&mockDBProvider{db: openJSONColumnDB(t)}, Config{
		SourceID: "test-source",
		Cron:     "* * * * * *",
		Queries:  `["SELECT id, meta, note FROM orders ORDER BY id"]`,
	})
	source.SetLogger(telemetry.NewDefaultLogger())
	defer source.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	msg, err := source.Read(ctx)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	assertDecodedOrderRow(t, msg.Data())
}

// The watermark is the persisted cursor, and it is built by formatting the
// column's value with %v. Decoding must not reach it: a map formats as
// "map[a:1]" rather than as the text the previous run compared against, which
// would break resume for anyone whose incremental column the driver happens to
// name JSON.
func TestTheWatermarkIsUnaffectedByJSONDecoding(t *testing.T) {
	db := openJSONColumnDB(t)
	if _, err := db.ExecContext(t.Context(),
		`INSERT INTO orders (id, meta, note) VALUES (2, '{"addr":{"city":"Paris"}}', 'x')`); err != nil {
		t.Fatal(err)
	}

	source := NewBatchSQLSource(&mockDBProvider{db: db}, Config{
		SourceID:          "test-source",
		Cron:              "* * * * * *",
		Queries:           `["SELECT id, meta, note FROM orders WHERE id > '{{.last_value}}' OR '{{.last_value}}' = '' ORDER BY id"]`,
		IncrementalColumn: "meta",
	})
	source.SetLogger(telemetry.NewDefaultLogger())
	defer source.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	msg, err := source.Read(ctx)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if err := source.Ack(ctx, msg); err != nil {
		t.Fatalf("Ack: %v", err)
	}

	if got := source.GetState()["last_value"]; got != `{"addr":{"city":"London"},"vip":true}` {
		t.Errorf("last_value = %q, want the column's text -- the cursor must not change shape "+
			"just because the value is now decoded for the message", got)
	}
}
