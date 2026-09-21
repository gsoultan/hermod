package sqlutil

import (
	"reflect"
	"testing"
)

// A PostgreSQL array reached the pipeline as its text literal -- the string
// "{1,2,3}" rather than a list -- on every path that reads through
// database/sql: db_lookup in both modes, batch_sql, and the editor's SQL
// builder whenever a query binds arguments.
//
// That is what a user reported as "real data shows null / no data found". Once
// an array is a string, every path into it resolves to nothing: a whereClause
// of `cust_code = {{.tags.0}}` binds nil, the query matches no row, and
// db_lookup's miss policy passes the message through unchanged. Nothing errors,
// because nothing failed.
//
// The carrier is the trap. pgx's database/sql driver routes json and jsonb
// through a []byte scan plan, but everything else -- arrays included -- falls
// to its `default: var d string` case. Measured against pgx v5.9.2 and
// PostgreSQL 18.4: a_nasty and n_money both arrive as Go *strings*. A fix
// written against the []byte carrier passes its unit tests and changes nothing
// at runtime, so every case here is built from a string on purpose.
func TestDecodeColumnParsesPostgresArrays(t *testing.T) {
	tests := []struct {
		name     string
		typeName string
		in       any
		want     any
	}{
		{"int array", "_INT4", "{1,2,3}", []any{int32(1), int32(2), int32(3)}},
		{"text array", "_TEXT", `{x,y}`, []any{"x", "y"}},
		{"empty array", "_INT4", "{}", []any{}},
		{"bool array", "_BOOL", "{t,f}", []any{true, false}},

		// The two literals that tell you whether a parser is right. A
		// hand-written split on "," gets both wrong, quietly: an embedded
		// comma, an embedded escaped quote, a brace inside a quoted element,
		// and -- the one that matters most -- a *quoted* NULL, which is the
		// four-character string and not a null.
		{
			"quoting and escapes",
			"_TEXT",
			`{"a,b","he said \"hi\"","{brace}","NULL"}`,
			[]any{"a,b", `he said "hi"`, "{brace}", "NULL"},
		},
		{
			"an unquoted NULL element is a real null",
			"_INT4",
			"{1,NULL,3}",
			[]any{int32(1), nil, int32(3)},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DecodeColumn(tc.in, tc.typeName)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("DecodeColumn(%q, %q)\n got %#v\nwant %#v", tc.in, tc.typeName, got, tc.want)
			}
		})
	}
}

// pgx's own array decoding flattens a multidimensional array, and the
// pgx-native path (PostgresSource.Sample, snapshot, polling) does the same --
// measured, both produce [1,2,3,4] for {{1,2},{3,4}}. Converging on that keeps
// the two paths in agreement, which is the point of this change; it is pinned
// here so that the dimensionality loss is a known, shared property rather than
// something discovered later on one path only.
func TestDecodeColumnFlattensAMultidimensionalArrayLikePgxDoes(t *testing.T) {
	got := DecodeColumn("{{1,2},{3,4}}", "_INT4")
	want := []any{int32(1), int32(2), int32(3), int32(4)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DecodeColumn = %#v, want %#v", got, want)
	}
}

// Anything that is not an array the driver can name stays exactly as it was.
func TestDecodeColumnLeavesEverythingElseAlone(t *testing.T) {
	tests := []struct {
		name     string
		typeName string
		in       any
		want     any
	}{
		// numeric is no longer text -- it became a json.Number so that the two
		// read paths agree on the shape pgx-native already produced, without
		// the precision loss a float64 would have caused. Its own cases live
		// in numeric_test.go; what belongs here is the boundary, which is that
		// a non-finite numeric is still refused and still arrives as text.
		{"a non-finite numeric stays text", "NUMERIC", "Infinity", "Infinity"},
		{"text is text", "TEXT", "{not,an,array}", "{not,an,array}"},
		// A text column holding decimal-looking digits must not be reshaped:
		// the rule is the database's own type name, never the content.
		{"decimal-looking text is not a number", "TEXT", "1200.50", "1200.50"},
		{"a brace-looking text column is not parsed", "VARCHAR", "{a,b}", "{a,b}"},
		{"time stays text", "TIME", "08:30:00", "08:30:00"},

		// money has no registered pgtype name and reports its bare OID. A
		// name-keyed rule must not choke on that.
		{"an unnamed type reports its OID", "790", "$19.99", "$19.99"},
		{"no type name at all", "", "{1,2}", "{1,2}"},

		{"driver bytes still become a string", "TEXT", []byte("hello"), "hello"},
		{"a non-string value is untouched", "INT8", int64(7), int64(7)},
		{"null stays null", "_INT4", nil, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DecodeColumn(tc.in, tc.typeName); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("DecodeColumn(%#v, %q) = %#v, want %#v", tc.in, tc.typeName, got, tc.want)
			}
		})
	}
}

// A literal the parser cannot make sense of must come back as the string it
// was. Returning nil would turn a malformed value into a missing one, which is
// the failure this whole change exists to remove.
func TestDecodeColumnKeepsAnUnparseableArrayAsText(t *testing.T) {
	if got := DecodeColumn("{1,2", "_INT4"); got != "{1,2" {
		t.Errorf("DecodeColumn = %#v, want the string unchanged", got)
	}
	if got := DecodeColumn("{a,b}", "_INT4"); got != "{a,b}" {
		t.Errorf("DecodeColumn = %#v, want the string unchanged", got)
	}
}

// JSON keeps working through the name-keyed entry point.
func TestDecodeColumnStillDecodesJSON(t *testing.T) {
	got, ok := DecodeColumn([]byte(`{"a":1}`), "JSONB").(map[string]any)
	if !ok {
		t.Fatalf("DecodeColumn = %#v, want a map", got)
	}
	if got["a"] != float64(1) {
		t.Errorf("a = %#v, want 1", got["a"])
	}
}
