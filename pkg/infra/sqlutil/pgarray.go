package sqlutil

import (
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgtype"
)

// PostgreSQL array support for the generic database/sql read path.
//
// pgx's database/sql driver decodes json, jsonb, bool, the integer and float
// widths, date and the timestamps; everything else falls to its
// `default: var d string` case. So an array arrives as its text literal --
// "{1,2,3}" -- while the very same column read through pgx natively
// (PostgresSource.ExecuteSQL, and the source's snapshot and polling paths)
// arrives as a []any. Once it is a string, every path into it resolves to
// nothing, which is what a whole class of "the field is null" reports turn out
// to be.
//
// The parsing is pgx's own, not a hand-rolled split on ",". A PostgreSQL array
// literal quotes elements containing commas, braces or quotes, escapes quotes
// inside them, and distinguishes an unquoted NULL element from the quoted
// four-character string "NULL". A hand parser gets those wrong quietly, and
// quietly wrong is the failure mode this change exists to remove.

// arrayMaps hands out *pgtype.Map values. A Map memoises scan plans as it
// works and is explicitly not safe for concurrent use -- pgx gives each
// connection its own -- while ScanRows is called from every worker at once.
// Pooling rather than locking keeps a hot read path from serialising on one
// mutex, and rather than building a Map per row, which registers the whole
// type table each time.
var arrayMaps = sync.Pool{New: func() any { return pgtype.NewMap() }}

// IsPGArrayTypeName reports whether a database's own name for a column type
// means the column holds a PostgreSQL array.
//
// pgx names an array by prefixing its element type with an underscore, so an
// int[] column reports "_INT4" and a text[] column "_TEXT". Dimensionality is
// not in the name: int[] and int[][] both report "_INT4".
//
// A type pgx has no registered name for reports its bare OID instead -- money
// comes back as "790" -- so a name-keyed rule has to tolerate names that
// describe nothing.
func IsPGArrayTypeName(databaseTypeName string) bool {
	return strings.HasPrefix(databaseTypeName, "_") && len(databaseTypeName) > 1
}

// DecodePGArray turns a PostgreSQL array literal into a list.
//
// Returns ok=false when the type name is not one pgx knows or the literal does
// not parse, and the caller keeps the text it already had. A malformed value
// must stay a malformed value: returning nil would turn it into a missing one,
// which is the bug being fixed, not a smaller version of it.
func DecodePGArray(raw []byte, typeName string) (any, bool) {
	m, _ := arrayMaps.Get().(*pgtype.Map)
	defer arrayMaps.Put(m)

	typ, ok := m.TypeForName(strings.ToLower(typeName))
	if !ok {
		return nil, false
	}
	v, err := typ.Codec.DecodeValue(m, typ.OID, pgtype.TextFormatCode, raw)
	if err != nil {
		return nil, false
	}
	return v, true
}
