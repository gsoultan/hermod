package sqlutil

import (
	"encoding/json"
	"testing"
)

// A numeric column used to reach the generic scan's callers as text while the
// pgx-native path carried it as an exact JSON number, so one column had two
// shapes decided by which path fetched it. json.Number closes that without the
// precision loss a float64 would introduce.
func TestDecodeColumnGivesANumericColumnItsExactNumericShape(t *testing.T) {
	for _, tc := range []struct {
		name     string
		in       any
		typeName string
		wantJSON string
	}{
		// The carrier that matters: pgx's database/sql driver routes numeric
		// through its default string case, not a []byte scan plan.
		{"scale is preserved", "1200.50", "NUMERIC", `1200.50`},
		{"beyond float64's exact range", "1.00000000000000000001", "NUMERIC", `1.00000000000000000001`},
		{"38 digits", "12345678901234567890123456789012345678", "NUMERIC", `12345678901234567890123456789012345678`},
		{"negative", "-0.125", "NUMERIC", `-0.125`},
		{"zero", "0", "NUMERIC", `0`},
		{"MySQL names it DECIMAL", []byte("42.00"), "DECIMAL", `42.00`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := DecodeColumn(tc.in, tc.typeName)

			n, ok := got.(json.Number)
			if !ok {
				t.Fatalf("DecodeColumn(%v, %q) = %#v (%T), want json.Number",
					tc.in, tc.typeName, got, got)
			}

			// The point of json.Number is what it serialises to: a bare JSON
			// number with every digit intact. Asserting on the Go value alone
			// would pass for a type that marshals as a quoted string.
			b, err := json.Marshal(n)
			if err != nil {
				t.Fatalf("marshalling the decoded value: %v", err)
			}
			if string(b) != tc.wantJSON {
				t.Errorf("marshalled to %s, want %s", b, tc.wantJSON)
			}
		})
	}
}

// PostgreSQL numeric accepts NaN, Infinity and -Infinity. None of the three has
// a JSON form, and json.Number does not validate on construction -- so handing
// them over as one would produce a value that fails json.Marshal, and a single
// such cell turns the whole message into a marshalling error rather than a row
// with one odd column.
//
// Text is the right answer here and it is also what the generic path already
// produced. The pgx-native path does NOT agree: measured on PostgreSQL 18.4 it
// marshals both infinities to 0, which is silent corruption, so converging on
// it for these three would make the generic path worse.
func TestDecodeColumnLeavesANonFiniteNumericAsText(t *testing.T) {
	for _, in := range []string{"NaN", "Infinity", "-Infinity"} {
		t.Run(in, func(t *testing.T) {
			got := DecodeColumn(in, "NUMERIC")

			s, ok := got.(string)
			if !ok {
				t.Fatalf("DecodeColumn(%q, NUMERIC) = %#v (%T), want the string unchanged", in, got, got)
			}
			if s != in {
				t.Errorf("got %q, want %q", s, in)
			}

			// The reason this matters: proving the rejected form would have
			// broken marshalling had it been converted.
			if _, err := json.Marshal(json.Number(in)); err == nil {
				t.Errorf("json.Number(%q) marshalled without error -- "+
					"the guard this test protects is no longer needed", in)
			}
		})
	}
}

// Anything the JSON number grammar does not accept stays text, whatever the
// column is named. A value that cannot be represented must reach the sink
// unchanged rather than be dropped or turned into an error.
func TestDecodeColumnLeavesUnparseableNumericText(t *testing.T) {
	for _, in := range []string{"", " ", "1,200.50", "0x1f", "1.2.3", "+5", "five"} {
		if got := DecodeColumn(in, "NUMERIC"); got != any(in) {
			t.Errorf("DecodeColumn(%q, NUMERIC) = %#v, want the string unchanged", in, got)
		}
	}
}

// A driver that already decoded the column is left alone: SQLite stores the
// declared type verbatim, so a column declared NUMERIC can arrive as a float64
// or an int64, and re-wrapping those would change a shape that was never wrong.
func TestDecodeColumnLeavesAlreadyTypedNumericValues(t *testing.T) {
	for _, in := range []any{float64(1.5), int64(42), nil, true} {
		if got := DecodeColumn(in, "NUMERIC"); got != in {
			t.Errorf("DecodeColumn(%#v, NUMERIC) = %#v, want it unchanged", in, got)
		}
	}
}

// The narrowness is the safety property: only a column the database itself
// names as a decimal type is reshaped. Deciding from content would turn every
// text column holding "42" into a number.
func TestIsNumericColumnTypeIsNarrow(t *testing.T) {
	for _, name := range []string{"NUMERIC", "numeric", "DECIMAL", "decimal"} {
		if !IsNumericColumnType(name) {
			t.Errorf("IsNumericColumnType(%q) = false, want true", name)
		}
	}
	// FLOAT8/REAL are already delivered as float64 by every driver here, and
	// MONEY carries a currency symbol that is not a JSON number at all.
	for _, name := range []string{"TEXT", "VARCHAR", "INT4", "FLOAT8", "REAL",
		"DOUBLE PRECISION", "MONEY", "790", "", "JSONB"} {
		if IsNumericColumnType(name) {
			t.Errorf("IsNumericColumnType(%q) = true, want false", name)
		}
	}
}
