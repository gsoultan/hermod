package sqlutil

import (
	"reflect"
	"testing"
)

// A JSON-typed column has to reach the pipeline as the value it holds, not as
// the text the wire happened to carry it in.
//
// Every SQL source turns driver []byte into string and stops there. That is
// right for text and wrong for a column the database itself types as JSON:
// PostgreSQL jsonb read through pgx already arrives as map[string]any on the
// snapshot path, so a source that stringifies it on another path contradicts
// itself -- and the workflow editor, which builds its field list from a sample
// taken on the first path, offers nested paths the second path cannot resolve.
//
// The caller decides *which* columns are JSON, from the database's own type
// metadata. This function only decides what the bytes mean. Applying it to an
// untyped text column would turn the string "123" into the number 123.
func TestDecodeJSONColumn(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want any
	}{
		{"object", `{"a":1,"b":{"c":"x"}}`, map[string]any{"a": float64(1), "b": map[string]any{"c": "x"}}},
		{"array", `["a","b"]`, []any{"a", "b"}},
		{"number", `42`, float64(42)},
		{"string", `"hello"`, "hello"},
		{"bool", `true`, true},
		{"json null", `null`, nil},

		// Not valid JSON: keep the bytes rather than lose them. A column that
		// cannot be decoded must still reach the sink.
		{"invalid", `{not json`, `{not json`},
		{"empty", ``, ``},
		{"trailing garbage", `{"a":1} oops`, `{"a":1} oops`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := DecodeJSONColumn([]byte(tc.raw))
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("DecodeJSONColumn(%q) = %#v (%T), want %#v (%T)",
					tc.raw, got, got, tc.want, tc.want)
			}
		})
	}
}

// The decoded value must not alias the driver's buffer. database/sql reuses the
// []byte it hands to Scan between rows, so a value that points into it becomes
// whatever the next row holds.
func TestDecodeJSONColumnDoesNotAliasTheBuffer(t *testing.T) {
	buf := []byte(`{"a":"first"}`)
	got := DecodeJSONColumn(buf)
	copy(buf, []byte(`{"a":"XXXXX"}`))

	obj, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("got %T, want map[string]any", got)
	}
	if obj["a"] != "first" {
		t.Errorf("decoded value changed to %#v when the caller reused the buffer", obj["a"])
	}
}

func TestDecodeJSONColumnKeepsInvalidTextIndependentOfTheBuffer(t *testing.T) {
	buf := []byte(`{not json`)
	got := DecodeJSONColumn(buf)
	copy(buf, []byte(`REPLACED!!`))
	if got != `{not json` {
		t.Errorf("fallback text = %#v, want the original bytes", got)
	}
}

// IsJSONColumnType is the caller's side of the split: which database type names
// mean "this column is JSON". Kept here so the sources agree.
func TestIsJSONColumnType(t *testing.T) {
	for _, name := range []string{"JSON", "json", "JSONB", "jsonb"} {
		if !IsJSONColumnType(name) {
			t.Errorf("IsJSONColumnType(%q) = false, want true", name)
		}
	}
	// A text column holding JSON is still a text column: SQLite and SQL Server
	// before 2025 have no JSON type, and guessing would change the shape of
	// every string that happens to parse.
	for _, name := range []string{"TEXT", "VARCHAR", "NVARCHAR", "CLOB", "BLOB", "", "JSONPATH"} {
		if IsJSONColumnType(name) {
			t.Errorf("IsJSONColumnType(%q) = true, want false", name)
		}
	}
}

// A driver may hand a JSON column over as a string rather than as []byte --
// go-mysql does exactly that for MYSQL_TYPE_JSON, where the column's declared
// type is unambiguously JSON but the value has already been converted. Asserting
// only on []byte silently skipped the decode and left the column a string, which
// the unit tests missed because they built []byte by hand and only the live
// server disagreed.
func TestDecodeValueHandlesBothStringAndBytes(t *testing.T) {
	const doc = `{"a":{"b":1}}`
	for _, tc := range []struct {
		name string
		in   any
	}{
		{"bytes", []byte(doc)},
		{"string", doc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := DecodeValue(tc.in, true)
			if _, ok := got.(map[string]any); !ok {
				t.Errorf("DecodeValue(%T, isJSON=true) = %#v (%T), want map[string]any",
					tc.in, got, got)
			}
		})
	}
}

// The same two carriers, when the column is not JSON, must come out as the
// string they always did.
func TestDecodeValueLeavesNonJSONAlone(t *testing.T) {
	if got := DecodeValue([]byte(`{"a":1}`), false); got != `{"a":1}` {
		t.Errorf("bytes: got %#v (%T), want the string", got, got)
	}
	if got := DecodeValue(`{"a":1}`, false); got != `{"a":1}` {
		t.Errorf("string: got %#v (%T), want it unchanged", got, got)
	}
	// Non-textual values are handed through untouched, JSON column or not.
	if got := DecodeValue(int64(7), true); got != int64(7) {
		t.Errorf("int: got %#v (%T), want int64(7)", got, got)
	}
	if got := DecodeValue(nil, true); got != nil {
		t.Errorf("nil: got %#v, want nil", got)
	}
}
