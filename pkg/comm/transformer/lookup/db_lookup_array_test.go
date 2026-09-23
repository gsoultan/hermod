package lookup

import (
	"database/sql"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
	_ "modernc.org/sqlite"
)

func arrayFixture(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE test (id TEXT PRIMARY KEY, n INTEGER, value TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO test(id, n, value) VALUES
		('u1',1,'one'), ('u2',2,'two'), ('u3',3,'three')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	return db
}

func strings2(t *testing.T, got any) []string {
	t.Helper()
	arr, ok := got.([]any)
	if !ok {
		t.Fatalf("want []any, got %#v", got)
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("want string element, got %#v", v)
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// The keyColumn path expanded []any/[]string/[]int/[]int64/[]float64 and
// nothing else, so a list that arrived as any other slice kind was bound whole
// to one placeholder and the driver rejected it.
func TestDBLookup_KeyColumn_ExpandsAnySliceKind(t *testing.T) {
	db := arrayFixture(t)
	src := storage.Source{Type: "sqlite"}
	tr := &DBLookupTransformer{}
	reg := fakeRegistry{db: db}

	for _, tc := range []struct {
		name string
		keys any
		col  string
		want []string
	}{
		{"[]any", []any{"u1", "u3"}, "id", []string{"one", "three"}},
		{"[]string", []string{"u1", "u2"}, "id", []string{"one", "two"}},
		{"[]int32", []int32{1, 3}, "n", []string{"one", "three"}},
		{"[]int", []int{2}, "n", []string{"two"}},
		{"[]float64 from JSON", []any{float64(1), float64(2)}, "n", []string{"one", "two"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tr.lookupSQL(t.Context(), reg, src, "test", tc.col, tc.keys, "", "value", "", nil)
			if err != nil {
				t.Fatalf("lookupSQL: %v", err)
			}
			if diff := strings2(t, got); !reflect.DeepEqual(diff, tc.want) {
				t.Fatalf("got %v, want %v", diff, tc.want)
			}
		})
	}
}

// []byte is a blob column value, not a list of bytes: expanding it would build
// an IN list of integers against a scalar column.
func TestDBLookup_KeyColumn_ByteSliceStaysScalar(t *testing.T) {
	db := arrayFixture(t)
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE blobs (k BLOB PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO blobs(k, value) VALUES (X'726177', 'blob')`); err != nil {
		t.Fatal(err)
	}
	src := storage.Source{Type: "sqlite"}
	tr := &DBLookupTransformer{}
	reg := fakeRegistry{db: db}

	got, err := tr.lookupSQL(t.Context(), reg, src, "blobs", "k", []byte("raw"), "", "value", "", nil)
	if err != nil {
		t.Fatalf("lookupSQL: %v", err)
	}
	if s, _ := got.(string); s != "blob" {
		t.Fatalf("got %#v, want \"blob\"", got)
	}
}

// The whereClause path bound a single-token template to one placeholder with
// "=", so a list value there was both the wrong operator and an unencodable
// argument.
func TestDBLookup_WhereClause_ExpandsSliceToINList(t *testing.T) {
	db := arrayFixture(t)
	src := storage.Source{Type: "sqlite"}
	tr := &DBLookupTransformer{}
	reg := fakeRegistry{db: db}

	data := map[string]any{"ids": []any{"u1", "u3"}}
	got, err := tr.lookupSQL(t.Context(), reg, src, "test", "", nil, "id = {{.ids}}", "value", "", data)
	if err != nil {
		t.Fatalf("lookupSQL: %v", err)
	}
	if diff := strings2(t, got); !reflect.DeepEqual(diff, []string{"one", "three"}) {
		t.Fatalf("got %v, want [one three]", diff)
	}
}

func TestDBLookup_WhereClause_EmptySliceMatchesNothing(t *testing.T) {
	db := arrayFixture(t)
	src := storage.Source{Type: "sqlite"}
	tr := &DBLookupTransformer{}
	reg := fakeRegistry{db: db}

	data := map[string]any{"ids": []any{}}
	got, err := tr.lookupSQL(t.Context(), reg, src, "test", "", nil, "id = {{.ids}}", "value", "", data)
	if err != nil {
		t.Fatalf("lookupSQL: %v", err)
	}
	if got != nil {
		t.Fatalf("got %#v, want nil", got)
	}
}

func TestDBLookup_WhereClause_ScalarStillUsesEquality(t *testing.T) {
	db := arrayFixture(t)
	src := storage.Source{Type: "sqlite"}
	tr := &DBLookupTransformer{}
	reg := fakeRegistry{db: db}

	data := map[string]any{"id": "u2"}
	got, err := tr.lookupSQL(t.Context(), reg, src, "test", "", nil, "id = {{.id}}", "value", "", data)
	if err != nil {
		t.Fatalf("lookupSQL: %v", err)
	}
	if s, _ := got.(string); s != "two" {
		t.Fatalf("got %#v, want \"two\"", got)
	}
}

// Query mode is the shape the report opened with.
func TestDBLookup_QueryTemplate_INListWithArray(t *testing.T) {
	db := arrayFixture(t)
	src := storage.Source{Type: "sqlite"}
	tr := &DBLookupTransformer{}
	reg := fakeRegistry{db: db}

	data := map[string]any{"ids": []any{"u1", "u2"}}
	got, err := tr.lookupSQLWithTemplate(t.Context(), reg, src,
		"SELECT value FROM test WHERE id IN ({{.ids}}) ORDER BY id", "value", sqlutil.MapResolver(data))
	if err != nil {
		t.Fatalf("lookupSQLWithTemplate: %v", err)
	}
	if diff := strings2(t, got); !reflect.DeepEqual(diff, []string{"one", "two"}) {
		t.Fatalf("got %v, want [one two]", diff)
	}
}

// The whole user story in one test: a CDC row carries a comma-separated list of
// ids as a single string, data_conversion turns it into a list, and db_lookup
// uses it as an IN list. The handoff between the two nodes is the interesting
// part -- data_conversion writes through SetData, which preserves the Go type,
// and db_lookup reads msg.Data(), which is the same raw map.
func TestDBLookup_ArrayFromDataConversionFeedsINList(t *testing.T) {
	db := arrayFixture(t)
	src := storage.Source{Type: "sqlite"}
	reg := fakeRegistry{db: db}

	conv, ok := transformer.Get("data_conversion")
	if !ok {
		t.Fatal("data_conversion is not registered")
	}

	in := message.AcquireMessage()
	in.SetData("id_csv", "u1, u3")
	msg, err := conv.Transform(t.Context(), in, map[string]any{
		"field":       "id_csv",
		"targetField": "ids",
		"targetType":  "array",
	})
	if err != nil {
		t.Fatalf("data_conversion: %v", err)
	}
	if _, isSlice := msg.Data()["ids"].([]any); !isSlice {
		t.Fatalf("data_conversion wrote %T, not a list", msg.Data()["ids"])
	}

	tr := &DBLookupTransformer{}
	got, err := tr.lookupSQLWithTemplate(t.Context(), reg, src,
		"SELECT value FROM test WHERE id IN ({{.ids}}) ORDER BY id", "value", evaluator.MessageResolver(msg))
	if err != nil {
		t.Fatalf("lookupSQLWithTemplate: %v", err)
	}
	if diff := strings2(t, got); !reflect.DeepEqual(diff, []string{"one", "three"}) {
		t.Fatalf("got %v, want [one three]", diff)
	}
}

// The same handoff with an int column, which needs elementType to survive the
// evaluator's JSON normalization: "1,3" splits into strings, and a string
// compared to an INTEGER column matches nothing on a typed database.
func TestDBLookup_ArrayWithElementTypeIntFeedsINList(t *testing.T) {
	db := arrayFixture(t)
	src := storage.Source{Type: "sqlite"}
	reg := fakeRegistry{db: db}

	conv, _ := transformer.Get("data_conversion")
	in := message.AcquireMessage()
	in.SetData("n_csv", "1,3")
	msg, err := conv.Transform(t.Context(), in, map[string]any{
		"field":       "n_csv",
		"targetField": "ns",
		"targetType":  "array",
		"elementType": "int",
	})
	if err != nil {
		t.Fatalf("data_conversion: %v", err)
	}

	tr := &DBLookupTransformer{}
	got, err := tr.lookupSQLWithTemplate(t.Context(), reg, src,
		"SELECT value FROM test WHERE n IN ({{.ns}}) ORDER BY n", "value", evaluator.MessageResolver(msg))
	if err != nil {
		t.Fatalf("lookupSQLWithTemplate: %v", err)
	}
	if diff := strings2(t, got); !reflect.DeepEqual(diff, []string{"one", "three"}) {
		t.Fatalf("got %v, want [one three]", diff)
	}
}

// A list too long to expand must fail the node rather than reach the driver as
// a half-built statement.
func TestDBLookup_QueryTemplate_OversizedListIsRejected(t *testing.T) {
	db := arrayFixture(t)
	src := storage.Source{Type: "sqlite"}
	tr := &DBLookupTransformer{}
	reg := fakeRegistry{db: db}

	huge := make([]any, sqlutil.MaxListExpansion+1)
	for i := range huge {
		huge[i] = i
	}
	_, err := tr.lookupSQLWithTemplate(t.Context(), reg, src,
		"SELECT value FROM test WHERE id IN ({{.ids}})", "value", sqlutil.MapResolver(map[string]any{"ids": huge}))
	if err == nil {
		t.Fatal("want an error for an oversized list")
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Fatalf("error should explain the limit, got: %v", err)
	}
}
