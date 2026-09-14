package mysql

import (
	"testing"

	"github.com/go-mysql-org/go-mysql/schema"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
)

// MySQL's JSON column type has the same problem PostgreSQL's jsonb had: every
// path in this source turns driver []byte into a string and stops, so a column
// the database types as JSON reaches transformations and sinks as an opaque
// string. go-mysql hands the binlog value over as []byte holding JSON text
// (replication/row_event.go, MYSQL_TYPE_JSON), and go-sql-driver does the same
// for a SELECT, so both paths were wrong in the same way.
//
// The columns' own type metadata decides: schema.TYPE_JSON on the binlog path,
// DatabaseTypeName() on the query paths. Nothing is guessed from content -- a
// VARCHAR holding `{"a":1}` stays a string, because reshaping every string that
// happens to parse is a much bigger change than the bug.

const jsonDoc = `{"tier":"gold","addr":{"city":"Jakarta"},"tags":["a","b"]}`

func TestBinlogRowJSONColumnBecomesAnObject(t *testing.T) {
	cols := []schema.TableColumn{
		{Name: "id", Type: schema.TYPE_NUMBER},
		{Name: "meta", Type: schema.TYPE_JSON},
		{Name: "note", Type: schema.TYPE_STRING},
	}
	row := []any{int64(7), []byte(jsonDoc), []byte(`{"not":"decoded"}`)}

	got := binlogRowToData(cols, row)

	obj, ok := got["meta"].(map[string]any)
	if !ok {
		t.Fatalf("meta is %T, want map[string]any", got["meta"])
	}
	if addr, _ := obj["addr"].(map[string]any); addr["city"] != "Jakarta" {
		t.Errorf("meta.addr.city = %#v, want Jakarta", addr["city"])
	}

	// A string column holding JSON is still a string.
	if got["note"] != `{"not":"decoded"}` {
		t.Errorf("note = %#v (%T), want the raw string -- only JSON-typed "+
			"columns are decoded", got["note"], got["note"])
	}
	// And nothing else changes shape.
	if got["id"] != int64(7) {
		t.Errorf("id = %#v (%T), want int64(7)", got["id"], got["id"])
	}
}

func TestBinlogRowJSONNullAndInvalid(t *testing.T) {
	cols := []schema.TableColumn{
		{Name: "a", Type: schema.TYPE_JSON},
		{Name: "b", Type: schema.TYPE_JSON},
	}
	got := binlogRowToData(cols, []any{nil, []byte(`{broken`)})
	if v, present := got["a"]; !present || v != nil {
		t.Errorf("a = %#v present=%v, want nil", v, present)
	}
	if got["b"] != `{broken` {
		t.Errorf("b = %#v, want the undecodable text preserved", got["b"])
	}
}

// A row shorter than the column list must not panic. The binlog and the cached
// table schema can disagree after a DDL.
func TestBinlogRowShorterThanColumns(t *testing.T) {
	cols := []schema.TableColumn{
		{Name: "a", Type: schema.TYPE_JSON},
		{Name: "b", Type: schema.TYPE_STRING},
	}
	got := binlogRowToData(cols, []any{[]byte(`{"x":1}`)})
	if len(got) != 1 {
		t.Errorf("got %#v, want only the column the row carried", got)
	}
	if _, ok := got["a"].(map[string]any); !ok {
		t.Errorf("a = %T, want map[string]any", got["a"])
	}
}

// The query paths (Sample, snapshot, poll) use database/sql, where the type
// name comes from ColumnType.DatabaseTypeName().
func TestQueryRowJSONColumnBecomesAnObject(t *testing.T) {
	names := []string{"id", "meta", "note"}
	types := []string{"BIGINT", "JSON", "VARCHAR"}
	values := []any{int64(7), []byte(jsonDoc), []byte(`{"not":"decoded"}`)}

	got := sqlutil.RecordFromValues(names, types, values)

	if _, ok := got["meta"].(map[string]any); !ok {
		t.Errorf("meta is %T, want map[string]any", got["meta"])
	}
	if got["note"] != `{"not":"decoded"}` {
		t.Errorf("note = %#v, want the raw string", got["note"])
	}
	if got["id"] != int64(7) {
		t.Errorf("id = %#v, want int64(7)", got["id"])
	}
}

// Type metadata is not always available -- some drivers return an empty name,
// and a caller may have none at all. That must degrade to today's behaviour,
// not to a panic or a reshaped column.
func TestQueryRowWithoutTypeMetadata(t *testing.T) {
	got := sqlutil.RecordFromValues([]string{"meta"}, nil, []any{[]byte(jsonDoc)})
	if got["meta"] != jsonDoc {
		t.Errorf("meta = %#v, want the raw string when no type metadata is available", got["meta"])
	}
}
