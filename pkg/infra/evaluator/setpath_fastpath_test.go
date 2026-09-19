package evaluator

// SetValByPath is the write-side twin of GetValByPath and had the same shape:
// marshal the whole map to JSON, sjson-set one field, unmarshal it all back,
// then clear the map and copy every key into it. 44us and 694 allocations to
// write one field of a 128-column row.
//
// It is riskier to change than the read side, because the round trip has a
// second effect nobody asked for: it JSON-normalises every *untouched* value in
// the map as well — int to float64, []byte to base64, a struct to a map. A
// targeted write cannot reproduce that.
//
// So the fast path is taken only when the map is already all-JSON-native, which
// is exactly the case where the round trip would have changed nothing else. Any
// other map still goes the long way round, and this test holds the two
// together over both.

import (
	"encoding/json"
	"maps"
	"reflect"
	"testing"

	"github.com/tidwall/sjson"
)

// referenceSetValByPath is the original implementation, kept verbatim as the
// oracle the fast path is compared against.
func referenceSetValByPath(data map[string]any, path string, val any) {
	if path == "" {
		return
	}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	newJSON, err := sjson.SetBytes(jsonData, path, val)
	if err != nil {
		return
	}
	var newData map[string]any
	if err := json.Unmarshal(newJSON, &newData); err == nil {
		for k := range data {
			delete(data, k)
		}
		maps.Copy(data, newData)
	}
}

func setParityRows() map[string]func() map[string]any {
	return map[string]func() map[string]any{
		// Already JSON-native: the fast path's target case.
		"native": func() map[string]any {
			return map[string]any{
				"s": "text", "f": 2.5, "b": true, "n": nil,
				"m":   map[string]any{"x": float64(1), "deep": map[string]any{"y": "z"}},
				"arr": []any{float64(1), "a", true},
			}
		},
		// Non-native values: must keep going the long way, because the round
		// trip rewrites these too.
		"ints": func() map[string]any {
			return map[string]any{"i": 42, "i64": int64(7), "s": "keep"}
		},
		"bytes": func() map[string]any {
			return map[string]any{"b": []byte("hi"), "s": "keep"}
		},
		"typed containers": func() map[string]any {
			return map[string]any{"mss": map[string]string{"k": "v"}, "ints": []int{1, 2}}
		},
		"nested non-native": func() map[string]any {
			return map[string]any{"m": map[string]any{"deep": map[string]any{"i": 3}}}
		},
		"array of non-native": func() map[string]any {
			return map[string]any{"arr": []any{1, 2}}
		},
		"empty": func() map[string]any { return map[string]any{} },
		"scalar in the way": func() map[string]any {
			return map[string]any{"a": "not-a-map"}
		},
	}
}

func setParityCases() []struct {
	path string
	val  any
} {
	return []struct {
		path string
		val  any
	}{
		{"", "ignored"},
		{"s", "new"},
		{"new_key", "created"},
		{"f", 99},
		{"f", 1.25},
		{"b", false},
		{"n", "no-longer-nil"},
		{"m.x", float64(5)},
		{"m.x", 5},
		{"m.deep.y", "changed"},
		{"m.deep.fresh", true},
		{"brand.new.nested.path", "deep"},
		{"a.b", "through a scalar"},
		{"arr.0", "replaced"},
		{"arr.-1", "appended"},
		{"arr", []any{float64(9)}},
		{"i", 1},
		{"b", []byte("raw")},
		{"m", map[string]any{"replaced": true}},
		{"with space", "spaced"},
		{"mss.k", "v2"},
		// Value types where the two encoders could disagree. sjson writes a
		// []byte as the literal string and json.Marshal writes base64; the
		// rest are here to find any other such pair rather than assume there
		// is none.
		{"v", []byte("raw")},
		{"v", json.RawMessage(`{"raw":1}`)},
		{"v", json.Number("12.5")},
		{"v", uint8(7)},
		{"v", int64(-9)},
		{"v", float32(1.5)},
		{"v", []string{"a", "b"}},
		{"v", map[string]string{"k": "v"}},
		{"v", []any{1, "two", []byte("x")}},
		{"v", map[string]any{"nested": []byte("y")}},
		{"v", struct {
			A int `json:"a"`
		}{A: 3}},
		{"v", (*int)(nil)},
		{"v", nil},
	}
}

// TestSetValByPathMatchesJSONRoundTrip is the contract: after the call, the map
// must be exactly what the round trip would have left behind — including what
// it did to fields the caller never named.
func TestSetValByPathMatchesJSONRoundTrip(t *testing.T) {
	for rowName, build := range setParityRows() {
		for _, tc := range setParityCases() {
			t.Run(rowName+"/"+tc.path, func(t *testing.T) {
				want := build()
				referenceSetValByPath(want, tc.path, tc.val)

				got := build()
				SetValByPath(got, tc.path, tc.val)

				if !reflect.DeepEqual(got, want) {
					t.Fatalf("SetValByPath(%s, %q, %#v)\n got: %#v\nwant: %#v",
						rowName, tc.path, tc.val, got, want)
				}
			})
		}
	}
}

// TestSetValByPathIsConstantCostOnNativeData pins the point of the change:
// writing one field of an already-decoded row must not get more expensive as
// the row gets wider. Before, a 128-column row cost 694 allocations.
func TestSetValByPathIsConstantCostOnNativeData(t *testing.T) {
	// benchRow holds ints, so normalise it the way a decoded message would be.
	nativeRow := func(cols int) map[string]any {
		raw := benchRow(cols)
		b, err := json.Marshal(raw)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var out map[string]any
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return out
	}

	narrow := nativeRow(8)
	wide := nativeRow(512)

	narrowAllocs := testing.AllocsPerRun(200, func() { SetValByPath(narrow, "col_4", "x") })
	wideAllocs := testing.AllocsPerRun(200, func() { SetValByPath(wide, "col_256", "x") })

	if wideAllocs > narrowAllocs+2 {
		t.Errorf("writing one field allocates %v on an 8-column row but %v on a 512-column row: cost still scales with row width",
			narrowAllocs, wideAllocs)
	}
	if wideAllocs > 4 {
		t.Errorf("writing one scalar field allocates %v times; want a small constant", wideAllocs)
	}
}

// A map that is not already JSON-native must keep taking the round trip,
// because that is the only thing that reproduces its side effect on the other
// fields. This is a guard against "optimising" the fast path into always-on.
func TestSetValByPathStillNormalisesNonNativeRows(t *testing.T) {
	row := map[string]any{"i": 42, "b": []byte("hi"), "s": "keep"}
	SetValByPath(row, "s", "changed")

	if got, ok := row["i"].(float64); !ok || got != 42 {
		t.Errorf(`row["i"] = %#v (%T), want float64(42): the untouched int must still be normalised`, row["i"], row["i"])
	}
	if got, ok := row["b"].(string); !ok || got != "aGk=" {
		t.Errorf(`row["b"] = %#v (%T), want "aGk=": the untouched []byte must still be base64`, row["b"], row["b"])
	}
	if row["s"] != "changed" {
		t.Errorf(`row["s"] = %#v, want "changed"`, row["s"])
	}
}
