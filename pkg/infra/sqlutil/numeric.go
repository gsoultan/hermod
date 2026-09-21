package sqlutil

import (
	"encoding/json"
	"strings"
)

// IsNumericColumnType reports whether a database's own name for a column type
// means the column holds an exact decimal.
//
// Deliberately narrow, for the same reason IsJSONColumnType is: only a column
// the database itself names as a decimal is reshaped. Deciding from content
// would turn every text column that happens to hold "42" into a number.
//
// FLOAT8, REAL and DOUBLE PRECISION are excluded because every driver here
// already delivers them as a float64 -- they were never text and have no
// precision left to preserve. MONEY is excluded because its text carries a
// currency symbol ("$19.99"), which is not a JSON number at all; it already
// agrees across both read paths as a string.
func IsNumericColumnType(databaseTypeName string) bool {
	switch strings.ToUpper(databaseTypeName) {
	case "NUMERIC", "DECIMAL":
		return true
	default:
		return false
	}
}

// DecodeNumericText gives an exact decimal column the shape it has on the
// pgx-native path: a bare JSON number carrying every digit the database sent.
//
// The bool reports whether it could. Text the JSON number grammar does not
// accept is returned unchanged, and that is not an edge case -- PostgreSQL
// numeric accepts NaN, Infinity and -Infinity, none of which has a JSON form.
// json.Number does not validate on construction, so converting one of those
// would build a value that fails json.Marshal, and a single such cell turns a
// whole message into a marshalling error rather than a row with one odd column.
//
// Text is also the better answer for those three on the merits: measured on
// PostgreSQL 18.4, pgtype.Numeric marshals both infinities to 0, so converging
// on the native shape there would replace a correct string with silent
// corruption.
func DecodeNumericText(s string) (json.Number, bool) {
	if !isJSONNumber(s) {
		return "", false
	}
	return json.Number(s), true
}

// isJSONNumber reports whether s is a complete JSON number.
//
// Hand-rolled rather than handed to encoding/json: this runs once per decimal
// cell per row, and a json.Decoder would allocate a reader and a decoder for
// every one of them. strconv.ParseFloat is not a substitute -- it accepts
// "NaN", "Inf" and hex floats, which is precisely the set that has to be
// rejected here.
//
// Grammar, from RFC 8259: [ '-' ] int [ frac ] [ exp ]. Note it rejects a
// leading '+', a leading zero such as "007", and a bare ".5" -- none of which
// PostgreSQL or MySQL emit for a decimal column, so rejecting them costs
// nothing and keeps this exactly as permissive as JSON itself.
func isJSONNumber(s string) bool {
	i := 0
	if i < len(s) && s[i] == '-' {
		i++
	}
	i, ok := scanJSONInt(s, i)
	if !ok {
		return false
	}
	if i = scanJSONFrac(s, i); i < 0 {
		return false
	}
	if i = scanJSONExp(s, i); i < 0 {
		return false
	}
	return i == len(s)
}

// scanDigits returns the index just past the run of digits starting at i.
func scanDigits(s string, i int) int {
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return i
}

// scanJSONInt reads the mandatory integer part: a single zero, or a non-zero
// digit followed by any digits. JSON rejects "007", so this does too.
func scanJSONInt(s string, i int) (int, bool) {
	start := i
	i = scanDigits(s, i)
	if i == start {
		return 0, false
	}
	if s[start] == '0' && i-start > 1 {
		return 0, false
	}
	return i, true
}

// scanJSONFrac reads an optional ".digits". Returns -1 if a dot is present
// without at least one digit after it.
func scanJSONFrac(s string, i int) int {
	if i >= len(s) || s[i] != '.' {
		return i
	}
	i++
	start := i
	if i = scanDigits(s, i); i == start {
		return -1
	}
	return i
}

// scanJSONExp reads an optional exponent. Returns -1 if an e/E is present
// without at least one digit after the optional sign.
func scanJSONExp(s string, i int) int {
	if i >= len(s) || (s[i] != 'e' && s[i] != 'E') {
		return i
	}
	i++
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	start := i
	if i = scanDigits(s, i); i == start {
		return -1
	}
	return i
}
