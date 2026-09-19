package evaluator

// GetValByPath used to marshal the entire row to JSON and parse it back with
// gjson to read one field, so reading a field cost O(row) rather than O(1) and
// a sink mapping with N placeholders cost O(N x row). Measured on a 128-column
// row: 17.9us and 294 allocations for a single field read, and 106us / 1763
// allocations to resolve a six-placeholder template.
//
// The round trip was not pure overhead, though: it is what normalises an int to
// a float64 and a []byte to a base64 string, and transformations, sink mappings
// and router conditions all depend on that shape. So the fast path has to be
// proved to return *exactly* what the round trip returned, which is what these
// tests are for.

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/tidwall/gjson"
)

// referenceGetValByPath is the original implementation, kept verbatim as the
// oracle the fast path is compared against.
func referenceGetValByPath(data map[string]any, path string) any {
	if path == "" {
		return nil
	}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil
	}
	res := gjson.GetBytes(jsonData, path)
	if !res.Exists() {
		return nil
	}
	return res.Value()
}

type customStringer struct {
	A int    `json:"a"`
	B string `json:"b"`
}

func parityRows() map[string]map[string]any {
	return map[string]map[string]any{
		"scalars": {
			"s": "text", "i": 42, "i8": int8(3), "i16": int16(4), "i32": int32(5),
			"i64": int64(6), "u": uint(7), "u64": uint64(8), "f32": float32(1.5),
			"f64": 2.5, "btrue": true, "bfalse": false, "nil": nil,
		},
		"containers": {
			"m":     map[string]any{"x": 1, "y": map[string]any{"z": "deep"}},
			"arr":   []any{1, "a", true, nil, map[string]any{"k": 2}},
			"empty": map[string]any{},
			"earr":  []any{},
		},
		"awkward": {
			"bytes":        []byte("hi"),
			"dotted.key":   "literal-dot",
			"":             "empty-key",
			"with space":   "spaced",
			"unicode_ключ": "u",
			"numstr":       "0123",
			"nested":       map[string]any{"0": "zero-as-key"},
		},
		"typed": {
			"strct":  customStringer{A: 1, B: "b"},
			"pstrct": &customStringer{A: 2, B: "c"},
			"nilptr": (*customStringer)(nil),
			"strs":   []string{"a", "b"},
			"ints":   []int{1, 2},
			"mss":    map[string]string{"k": "v"},
		},
	}
}

func parityPaths() []string {
	return []string{
		"", "s", "i", "i8", "i16", "i32", "i64", "u", "u64", "f32", "f64",
		"btrue", "bfalse", "nil", "missing", "m", "m.x", "m.y", "m.y.z",
		"m.y.missing", "m.missing.z", "arr", "arr.0", "arr.1", "arr.3",
		"arr.4", "arr.4.k", "arr.9", "empty", "empty.x", "earr", "earr.0",
		"bytes", "dotted.key", "with space", "unicode_ключ", "numstr",
		"nested.0", "strct", "strct.a", "pstrct.b", "nilptr", "strs",
		"strs.1", "ints.0", "mss.k", "s.x", "i.0",
	}
}

// TestGetValByPathMatchesJSONRoundTrip is the contract: whatever the fast path
// does, the answer must be byte-for-byte the answer the JSON round trip gave,
// including its type normalisation.
func TestGetValByPathMatchesJSONRoundTrip(t *testing.T) {
	for rowName, row := range parityRows() {
		for _, path := range parityPaths() {
			t.Run(rowName+"/"+path, func(t *testing.T) {
				want := referenceGetValByPath(row, path)
				got := GetValByPath(row, path)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("GetValByPath(%s, %q) = %#v (%T), reference = %#v (%T)",
						rowName, path, got, got, want, want)
				}
			})
		}
	}
}

// TestGetValByPathIsConstantCost pins the actual point of the change: reading
// one field must not get more expensive as the row gets wider. Before the fast
// path a 128-column row cost 294 allocations per read against 24 for an
// 8-column one.
func TestGetValByPathIsConstantCost(t *testing.T) {
	narrow := benchRow(8)
	wide := benchRow(512)

	narrowAllocs := testing.AllocsPerRun(200, func() { GetValByPath(narrow, "col_4") })
	wideAllocs := testing.AllocsPerRun(200, func() { GetValByPath(wide, "col_256") })

	if wideAllocs > narrowAllocs+2 {
		t.Errorf("reading one field allocates %v on an 8-column row but %v on a 512-column row: cost still scales with row width",
			narrowAllocs, wideAllocs)
	}
	if wideAllocs > 4 {
		t.Errorf("reading one scalar field allocates %v times; want a small constant", wideAllocs)
	}
}
