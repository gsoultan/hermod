package sqlutil

import (
	"database/sql"
	"encoding/json"
	"strings"
)

// DecodeJSONColumn gives the bytes of a JSON-typed column the value they
// represent, so a `jsonb`/`json` column reaches transformations and sinks as an
// object rather than as a string that happens to contain one.
//
// Only call this for columns the database itself types as JSON -- see
// IsJSONColumnType, or the source's own type metadata. Applied to a text column
// it would turn the string "123" into the number 123 and "true" into a boolean.
//
// Bytes that do not parse are returned as a string. A column that cannot be
// decoded still has to reach the sink; dropping it, or failing the message, is
// how this class of bug cost data in the first place.
func DecodeJSONColumn(raw []byte) any {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	// json.Unmarshal copies every string it produces, but a []byte destination
	// inside the document would alias raw, and database/sql reuses the buffer it
	// passes to Scan between rows. Decoding into `any` never yields []byte, so
	// the result is already independent -- except for the fallback above, which
	// string() copies.
	return v
}

// IsJSONColumnType reports whether a database's own name for a column type
// means the column holds JSON.
//
// Deliberately narrow. SQLite has no JSON type and SQL Server had none before
// 2025, so JSON there lives in TEXT/NVARCHAR columns; treating those as JSON
// would reshape every string that happens to parse, which is a far larger
// change than the bug being fixed.
func IsJSONColumnType(databaseTypeName string) bool {
	switch strings.ToUpper(databaseTypeName) {
	case "JSON", "JSONB":
		return true
	default:
		return false
	}
}

// ColumnTypeNames reads the driver's own name for each column, in order.
//
// Returns nil when the driver cannot say. Callers treat that as "no JSON
// columns", which is the behaviour every source had before JSON columns were
// decoded at all -- a driver that withholds type metadata must not change a
// pipeline's shape.
func ColumnTypeNames(rows *sql.Rows) []string {
	cts, err := rows.ColumnTypes()
	if err != nil {
		return nil
	}
	names := make([]string, len(cts))
	for i, ct := range cts {
		names[i] = ct.DatabaseTypeName()
	}
	return names
}

// RecordFromValues builds one row map from a scanned row.
//
// Driver []byte becomes a string, as it always has, except where typeNames
// says the column is JSON or a PostgreSQL array -- there it becomes the value
// the literal describes, matching what pgx already produces for those columns
// on its own paths.
//
// typeNames may be nil or shorter than names.
func RecordFromValues(names, typeNames []string, values []any) map[string]any {
	record := make(map[string]any, len(names))
	for i, name := range names {
		if i >= len(values) {
			break
		}
		typeName := ""
		if i < len(typeNames) {
			typeName = typeNames[i]
		}
		record[name] = DecodeColumn(values[i], typeName)
	}
	return record
}

// DecodeColumn is the per-value rule, decided from the database's own name for
// the column's type.
//
// This is the entry point for any caller that has the driver's type metadata.
// DecodeValue remains for the one that does not: the MySQL binlog reader knows
// only a go-mysql column enum, never a type name.
//
// Deliberately narrow, and for the same reason in both branches: only a column
// the database itself calls JSON or an array is reshaped. Deciding from content
// would reshape every string that happens to look like one.
//
// numeric becomes a json.Number, not a float64. The pgx-native path carries it
// exactly -- measured on PostgreSQL 18.4, a numeric(40,20) holding
// 1.00000000000000000001 marshals from pgtype.Numeric at full precision -- so
// converting to float64 would have flattened it to 1 and propagated a loss
// rather than closed a gap. json.Number keeps every digit and serialises as the
// bare number the native path produces, which is what makes the two agree.
//
// Non-finite numerics stay text: PostgreSQL accepts NaN, Infinity and
// -Infinity, and DecodeNumericText refuses all three because json.Number does
// not validate and one such cell would fail the whole message's marshalling.
func DecodeColumn(v any, typeName string) any {
	if IsPGArrayTypeName(typeName) {
		var raw []byte
		switch b := v.(type) {
		case []byte:
			raw = b
		case string:
			// The carrier that matters. pgx's database/sql driver routes only
			// json, jsonb, bytea and xml through a []byte scan plan; an array
			// falls to its default string case, so a []byte-only branch here
			// would never fire at runtime however green its tests were.
			raw = []byte(b)
		default:
			return v
		}
		if decoded, ok := DecodePGArray(raw, typeName); ok {
			return decoded
		}
		return string(raw)
	}
	if IsNumericColumnType(typeName) {
		// Same carrier note as the array branch: pgx hands numeric over as a
		// string, MySQL's driver as []byte. A value the driver already typed
		// (SQLite stores the declared type verbatim, so a column declared
		// NUMERIC can arrive as a float64) falls through untouched -- it was
		// never text and has no precision left to preserve.
		var text string
		switch n := v.(type) {
		case string:
			text = n
		case []byte:
			text = string(n)
		default:
			return v
		}
		if num, ok := DecodeNumericText(text); ok {
			return num
		}
		return text
	}
	return DecodeValue(v, IsJSONColumnType(typeName))
}

// DecodeValue is the per-value rule RecordFromValues applies, exposed for the
// callers that need to keep hold of a single column as they go.
// Both carriers matter. database/sql drivers hand text over as []byte, but
// go-mysql has already converted a MYSQL_TYPE_JSON value to a string by the
// time the binlog handler sees it -- asserting only on []byte left that column
// a string, and no unit test built from hand-written []byte could notice.
func DecodeValue(v any, isJSON bool) any {
	switch b := v.(type) {
	case []byte:
		if isJSON {
			return DecodeJSONColumn(b)
		}
		return string(b)
	case string:
		if isJSON {
			return DecodeJSONColumn([]byte(b))
		}
		return b
	default:
		return v
	}
}
