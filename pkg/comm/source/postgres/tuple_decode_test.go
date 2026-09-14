package postgres

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/jackc/pglogrepl"
	"github.com/jackc/pgx/v5/pgtype"
)

// The pgoutput tuple decoder is the only place a live CDC row is turned into
// Go values, and for years it did exactly two things: `string(col.Data)` for
// text and `col.Data` for binary. That is wrong in two ways that cost data.
//
//  1. A json/jsonb column arrived as a *string* holding JSON, while the very
//     same column read through Sample/snapshot arrives as map[string]any --
//     pgx's codec unmarshals it. The workflow editor builds its field list from
//     Sample, so it offered `meta.addr.city` on a pipeline where `meta` was one
//     opaque string and that path resolved to nil.
//  2. 'u' -- unchanged TOASTed value -- was not a case at all, so the column
//     was dropped from the row image entirely. jsonb TOASTs above ~2 KB, so an
//     ordinary UPDATE to any other column silently lost the document.
//
// These tests use synthetic tuples so they run in the default suite. The
// integration tests beside them prove the same thing against a real
// replication stream.

func rel(cols ...pglogrepl.RelationMessageColumn) *pglogrepl.RelationMessage {
	return &pglogrepl.RelationMessage{
		RelationName: "orders",
		Namespace:    "public",
		Columns:      colPtrs(cols),
	}
}

func colPtrs(cols []pglogrepl.RelationMessageColumn) []*pglogrepl.RelationMessageColumn {
	out := make([]*pglogrepl.RelationMessageColumn, len(cols))
	for i := range cols {
		c := cols[i]
		out[i] = &c
	}
	return out
}

func col(name string, oid uint32) pglogrepl.RelationMessageColumn {
	return pglogrepl.RelationMessageColumn{Name: name, DataType: oid}
}

func tuple(cols ...*pglogrepl.TupleDataColumn) *pglogrepl.TupleData {
	return &pglogrepl.TupleData{Columns: cols}
}

func text(s string) *pglogrepl.TupleDataColumn {
	return &pglogrepl.TupleDataColumn{DataType: 't', Data: []byte(s)}
}
func null() *pglogrepl.TupleDataColumn  { return &pglogrepl.TupleDataColumn{DataType: 'n'} }
func toast() *pglogrepl.TupleDataColumn { return &pglogrepl.TupleDataColumn{DataType: 'u'} }

const doc = `{"tier":"gold","addr":{"city":"Jakarta"},"tags":["a","b"]}`

func TestTupleDecodeJSONBBecomesAnObject(t *testing.T) {
	r := rel(col("id", pgtype.Int4OID), col("meta", pgtype.JSONBOID))
	got, missing := decodeTuple(r, tuple(text("7"), text(doc)), nil)

	if len(missing) != 0 {
		t.Errorf("missing = %v, want none", missing)
	}
	obj, ok := got["meta"].(map[string]any)
	if !ok {
		t.Fatalf("meta is %T, want map[string]any -- a jsonb column must decode "+
			"to the same shape Sample produces", got["meta"])
	}
	addr, _ := obj["addr"].(map[string]any)
	if addr["city"] != "Jakarta" {
		t.Errorf("meta.addr.city = %#v, want Jakarta", addr["city"])
	}

	// Every other type keeps the shape it has always had. Widening this to all
	// types would change `id` from "7" to 7 for every existing workflow.
	if got["id"] != "7" {
		t.Errorf("id = %#v (%T), want the string \"7\" -- non-JSON columns must "+
			"not change shape", got["id"], got["id"])
	}
}

func TestTupleDecodeJSONColumnToo(t *testing.T) {
	r := rel(col("doc", pgtype.JSONOID))
	got, _ := decodeTuple(r, tuple(text(doc)), nil)
	if _, ok := got["doc"].(map[string]any); !ok {
		t.Errorf("json column decoded to %T, want map[string]any", got["doc"])
	}
}

// A jsonb column holding an array or a scalar is still valid JSON and must not
// be mangled into a string.
func TestTupleDecodeJSONBArrayAndScalar(t *testing.T) {
	r := rel(col("tags", pgtype.JSONBOID), col("n", pgtype.JSONBOID))
	got, _ := decodeTuple(r, tuple(text(`["a","b"]`), text(`42`)), nil)
	if _, ok := got["tags"].([]any); !ok {
		t.Errorf("tags is %T, want []any", got["tags"])
	}
	if got["n"] != float64(42) {
		t.Errorf("n = %#v, want 42", got["n"])
	}
}

// Invalid JSON in a json/jsonb column must not vanish and must not fail the
// message. Postgres will not normally store it, but a corrupt WAL record or a
// future type we misidentify should degrade to the old behaviour, not to a gap.
func TestTupleDecodeInvalidJSONFallsBackToText(t *testing.T) {
	r := rel(col("meta", pgtype.JSONBOID))
	got, _ := decodeTuple(r, tuple(text(`{not json`)), nil)
	if got["meta"] != `{not json` {
		t.Errorf("meta = %#v, want the raw text preserved", got["meta"])
	}
}

func TestTupleDecodeNullStaysNull(t *testing.T) {
	r := rel(col("meta", pgtype.JSONBOID))
	got, missing := decodeTuple(r, tuple(null()), nil)
	v, present := got["meta"]
	if !present || v != nil {
		t.Errorf("meta = %#v present=%v, want an explicit nil", v, present)
	}
	if len(missing) != 0 {
		t.Errorf("a NULL is not a missing value: missing = %v", missing)
	}
}

// The TOAST repair. Under REPLICA IDENTITY FULL the old tuple carries the full
// value even when the new tuple says 'u' -- verified against a live stream in
// TestJSONBToastUnchanged -- so the after-image can be completed from it.
func TestTupleDecodeUnchangedToastRecoveredFromOldTuple(t *testing.T) {
	r := rel(col("id", pgtype.Int4OID), col("email", pgtype.TextOID), col("meta", pgtype.JSONBOID))
	old := tuple(text("7"), text("a@x.com"), text(doc))
	newT := tuple(text("7"), text("b@x.com"), toast())

	got, missing := decodeTuple(r, newT, old)

	if len(missing) != 0 {
		t.Errorf("missing = %v, want none -- the old tuple had the value", missing)
	}
	if got["email"] != "b@x.com" {
		t.Errorf("email = %#v, want the new value", got["email"])
	}
	obj, ok := got["meta"].(map[string]any)
	if !ok {
		t.Fatalf("meta is %T, want the value carried over from the old tuple", got["meta"])
	}
	if obj["tier"] != "gold" {
		t.Errorf("meta.tier = %#v, want gold", obj["tier"])
	}
}

// REPLICA IDENTITY DEFAULT sends only the key columns in the old tuple, so
// there is nothing to recover from. Dropping the column silently is what caused
// the data loss; the column stays out of the image, but the caller is told.
func TestTupleDecodeUnrecoverableToastIsReported(t *testing.T) {
	r := rel(col("id", pgtype.Int4OID), col("meta", pgtype.JSONBOID))
	got, missing := decodeTuple(r, tuple(text("7"), toast()), nil)

	if _, present := got["meta"]; present {
		t.Errorf("meta = %#v, want it absent rather than guessed at", got["meta"])
	}
	if !reflect.DeepEqual(missing, []string{"meta"}) {
		t.Errorf("missing = %v, want [meta] so the caller can say so", missing)
	}
}

// An old tuple that is itself 'u' for that column is no better than no old
// tuple at all.
func TestTupleDecodeToastInBothTuplesIsReported(t *testing.T) {
	r := rel(col("meta", pgtype.JSONBOID))
	got, missing := decodeTuple(r, tuple(toast()), tuple(toast()))
	if _, present := got["meta"]; present {
		t.Errorf("meta = %#v, want it absent", got["meta"])
	}
	if !reflect.DeepEqual(missing, []string{"meta"}) {
		t.Errorf("missing = %v, want [meta]", missing)
	}
}

// A tuple with more columns than the relation describes must not panic; the
// existing decoders guarded on this and the guard has to survive the rewrite.
func TestTupleDecodeIgnoresColumnsBeyondTheRelation(t *testing.T) {
	r := rel(col("id", pgtype.Int4OID))
	got, _ := decodeTuple(r, tuple(text("7"), text("stray")), nil)
	if len(got) != 1 || got["id"] != "7" {
		t.Errorf("got %#v, want just id", got)
	}
}

// The whole point is that the after-image serialises as a real nested object.
func TestTupleDecodeMarshalsAsNestedJSON(t *testing.T) {
	r := rel(col("meta", pgtype.JSONBOID))
	got, _ := decodeTuple(r, tuple(text(doc)), nil)
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var round map[string]any
	if err := json.Unmarshal(b, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	meta, ok := round["meta"].(map[string]any)
	if !ok {
		t.Fatalf("after-image meta round-trips to %T, want an object: %s", round["meta"], b)
	}
	if addr, _ := meta["addr"].(map[string]any); addr["city"] != "Jakarta" {
		t.Errorf("after-image lost the nesting: %s", b)
	}
}
