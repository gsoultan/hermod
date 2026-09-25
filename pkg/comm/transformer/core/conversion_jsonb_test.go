package core

import (
	"testing"
	"time"
)

// A "jsonb" conversion renders a value as JSON *text*.
//
// That is the shape a json/jsonb column needs and the only shape every SQL
// driver can bind: database/sql rejects a map[string]any outright, and the
// PostgreSQL sink already does exactly this (marshalJSONValue,
// pkg/comm/sink/postgres/postgres.go:1204) on the paths where it knows the
// column type. A pipeline that builds an object in a `set` node, or reads one
// from a document source, had no way to get it into a jsonb column anywhere
// else.
//
// Text that already *is* a JSON object or array is passed through untouched
// rather than re-encoded -- re-encoding would quote it into a JSON string, the
// double-encoding bug this conversion exists to avoid. The object/array test is
// the same one toArray applies a few lines up, so the two agree on what "this
// string holds JSON" means.
func TestDataConversion_ToJSONB(t *testing.T) {
	cases := []struct {
		name string
		in   any
		cfg  map[string]any
		want any
	}{
		{"map becomes object text", map[string]any{"b": 2, "a": 1}, map[string]any{"targetType": "jsonb"}, `{"a":1,"b":2}`},
		{"slice becomes array text", []any{1, "x"}, map[string]any{"targetType": "jsonb"}, `[1,"x"]`},
		{"object text passes through", `{"a":1}`, map[string]any{"targetType": "jsonb"}, `{"a":1}`},
		{"array text passes through", `[1,2]`, map[string]any{"targetType": "jsonb"}, `[1,2]`},
		{"surrounding whitespace is trimmed", "  {\"a\":1}\n", map[string]any{"targetType": "jsonb"}, `{"a":1}`},
		{"plain text becomes a json string", "hello", map[string]any{"targetType": "jsonb"}, `"hello"`},
		{"text is not re-read as a number", "123", map[string]any{"targetType": "jsonb"}, `"123"`},
		{"text is not re-read as a bool", "true", map[string]any{"targetType": "jsonb"}, `"true"`},
		{"a quote is escaped", `say "hi"`, map[string]any{"targetType": "jsonb"}, `"say \"hi\""`},
		{"a number becomes a json number", 42, map[string]any{"targetType": "jsonb"}, `42`},
		{"a bool becomes a json bool", true, map[string]any{"targetType": "jsonb"}, `true`},
		{"json is an alias for jsonb", map[string]any{"a": 1}, map[string]any{"targetType": "json"}, `{"a":1}`},
		{"empty string is an empty json string", "", map[string]any{"targetType": "jsonb"}, `""`},
		{"empty object stays an empty object", `{}`, map[string]any{"targetType": "jsonb"}, `{}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := convert(t, tc.in, tc.cfg)
			if err != nil {
				t.Fatalf("convert: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

// Text that opens like an object or array but does not parse is a broken
// payload, not a value to be quoted. Quoting it would store the mangled text in
// the jsonb column and report success; failing puts it under errorBehavior,
// which is where the operator's choice already lives.
func TestDataConversion_ToJSONB_MalformedJSONFollowsErrorBehavior(t *testing.T) {
	// Truncated: the closing brace never arrived. Quoting this would store
	// `"{\"a\":1"` and call it a success.
	broken := `{"a":1`

	// Text that opens like JSON but was never JSON is the same answer, rather
	// than being quoted. It is the deliberate cost of catching truncation: a
	// value like this is either a mistake worth seeing or a field that should
	// not be converted at all.
	for _, notJSON := range []string{`[redacted]`, `{TBD}`} {
		if _, err := convert(t, notJSON, map[string]any{"targetType": "jsonb"}); err == nil {
			t.Errorf("want an error for %q, got nil", notJSON)
		}
	}

	if _, err := convert(t, broken, map[string]any{"targetType": "jsonb"}); err == nil {
		t.Fatal("want an error for malformed JSON object text, got nil")
	}

	got, err := convert(t, broken, map[string]any{"targetType": "jsonb", "errorBehavior": "null"})
	if err != nil {
		t.Fatalf(`errorBehavior "null": %v`, err)
	}
	if got != nil {
		t.Errorf(`errorBehavior "null": got %#v, want nil`, got)
	}

	got, err = convert(t, broken, map[string]any{"targetType": "jsonb", "errorBehavior": "keep"})
	if err != nil {
		t.Fatalf(`errorBehavior "keep": %v`, err)
	}
	if got != broken {
		t.Errorf(`errorBehavior "keep": got %#v, want %#v`, got, broken)
	}
}

// Types the evaluator's JSON round-trip would rewrite before the transformer
// ever saw them, so they are driven through convertScalar directly.
func TestDataConversion_ToJSONB_GoTypes(t *testing.T) {
	tr := &DataConversionTransformer{}

	cases := []struct {
		name string
		in   any
		want string
	}{
		// A driver hands JSON text over as []byte. Marshalling it would
		// base64-encode it, which is how a jsonb column ends up holding
		// "eyJhIjoxfQ==".
		{"json bytes pass through as text", []byte(`{"a":1}`), `{"a":1}`},
		{"plain bytes become a json string", []byte("hello"), `"hello"`},
		{"time marshals to RFC3339", time.Date(2026, 9, 18, 10, 30, 0, 0, time.UTC), `"2026-09-18T10:30:00Z"`},
		{"typed map marshals", map[string]int{"a": 1}, `{"a":1}`},
		{"typed slice marshals", []string{"a", "b"}, `["a","b"]`},
		{"int64 keeps its precision", int64(9007199254740993), `9007199254740993`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tr.convertScalar(tc.in, conversionRow{targetType: "jsonb"})
			if err != nil {
				t.Fatalf("convertScalar: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %#v, want %#v", got, tc.want)
			}
		})
	}

	if _, err := tr.convertScalar([]byte(`[1,`), conversionRow{targetType: "jsonb"}); err == nil {
		t.Error("want an error for malformed JSON bytes, got nil")
	}
}

// jsonb is a scalar conversion, so it is available per element of an array --
// what a jsonb[] column needs.
func TestDataConversion_ToJSONB_AsElementType(t *testing.T) {
	got, err := convert(t, []any{map[string]any{"a": 1}, "x"}, map[string]any{
		"targetType":  "array",
		"elementType": "jsonb",
	})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	want := []any{`{"a":1}`, `"x"`}
	arr, ok := got.([]any)
	if !ok || len(arr) != len(want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	for i := range want {
		if arr[i] != want[i] {
			t.Errorf("element %d: got %#v, want %#v", i, arr[i], want[i])
		}
	}
}
