package avrodecode

import (
	"math"
	"math/big"
	"reflect"
	"testing"
	"time"

	"github.com/hamba/avro/v2"
)

// Correctness here is established differentially: encode with hamba, decode
// with this package, compare. That is the strongest signal available without a
// second decoder, and it is available precisely because the advisories are
// decoder-side only — hamba's *encoder* is not implicated and stays trusted.
//
// A decoder that is merely safe is not useful. A wrong one is worse than a
// crashing one, because it corrupts data silently.

func roundTrip(t *testing.T, schemaStr string, in map[string]any) map[string]any {
	t.Helper()

	schema := mustParse(t, schemaStr)
	encoded, err := avro.Marshal(schema, in)
	if err != nil {
		t.Fatalf("hamba Marshal: %v", err)
	}
	out, err := Decode(schema, encoded, DefaultLimits())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	return out
}

func TestPrimitivesRoundTrip(t *testing.T) {
	const schema = `{"type":"record","name":"P","fields":[
		{"name":"n","type":"null"},
		{"name":"b","type":"boolean"},
		{"name":"i","type":"int"},
		{"name":"l","type":"long"},
		{"name":"f","type":"float"},
		{"name":"d","type":"double"},
		{"name":"by","type":"bytes"},
		{"name":"s","type":"string"}]}`

	in := map[string]any{
		"n":  nil,
		"b":  true,
		"i":  int32(-42),
		"l":  int64(1) << 40,
		"f":  float32(1.5),
		"d":  2.25,
		"by": []byte{0x01, 0x02},
		"s":  "ada",
	}

	got := roundTrip(t, schema, in)

	// Avro's type system is preserved rather than widened: int is 32-bit and
	// long is 64-bit, and collapsing them would lose that distinction on the
	// way back out to a typed sink.
	want := map[string]any{
		"n": nil, "b": true, "i": int32(-42), "l": int64(1) << 40,
		"f": float32(1.5), "d": 2.25, "by": []byte{0x01, 0x02}, "s": "ada",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip =\n  %#v\nwant\n  %#v", got, want)
	}
}

// Zigzag varint is where an off-by-one hides. The boundaries are the values
// that change encoded length or sign handling.
func TestIntegerBoundariesRoundTrip(t *testing.T) {
	const schema = `{"type":"record","name":"I","fields":[{"name":"v","type":"long"}]}`

	for _, v := range []int64{
		0, -1, 1, 63, 64, -64, -65,
		math.MaxInt32, math.MinInt32,
		math.MaxInt64, math.MinInt64,
	} {
		got := roundTrip(t, schema, map[string]any{"v": v})
		if got["v"] != v {
			t.Errorf("long %d round-tripped to %#v", v, got["v"])
		}
	}
}

func TestNestedRecordRoundTrips(t *testing.T) {
	const schema = `{"type":"record","name":"Outer","fields":[
		{"name":"id","type":"long"},
		{"name":"inner","type":{"type":"record","name":"Inner","fields":[
			{"name":"name","type":"string"}]}}]}`

	got := roundTrip(t, schema, map[string]any{
		"id":    int64(1),
		"inner": map[string]any{"name": "ada"},
	})

	inner, ok := got["inner"].(map[string]any)
	if !ok {
		t.Fatalf("inner = %#v, want map[string]any", got["inner"])
	}
	if inner["name"] != "ada" {
		t.Errorf("inner.name = %#v, want ada", inner["name"])
	}
}

func TestArrayAndMapRoundTrip(t *testing.T) {
	const schema = `{"type":"record","name":"C","fields":[
		{"name":"tags","type":{"type":"array","items":"string"}},
		{"name":"counts","type":{"type":"map","values":"long"}}]}`

	got := roundTrip(t, schema, map[string]any{
		"tags":   []any{"x", "y", "z"},
		"counts": map[string]any{"a": int64(1), "b": int64(2)},
	})

	tags, ok := got["tags"].([]any)
	if !ok || len(tags) != 3 || tags[0] != "x" || tags[2] != "z" {
		t.Errorf("tags = %#v, want [x y z]", got["tags"])
	}
	counts, ok := got["counts"].(map[string]any)
	if !ok || counts["a"] != int64(1) || counts["b"] != int64(2) {
		t.Errorf("counts = %#v, want {a:1 b:2}", got["counts"])
	}
}

func TestEmptyArrayAndMapRoundTrip(t *testing.T) {
	const schema = `{"type":"record","name":"E","fields":[
		{"name":"tags","type":{"type":"array","items":"string"}},
		{"name":"counts","type":{"type":"map","values":"long"}}]}`

	got := roundTrip(t, schema, map[string]any{
		"tags": []any{}, "counts": map[string]any{},
	})

	if tags, ok := got["tags"].([]any); !ok || len(tags) != 0 {
		t.Errorf("tags = %#v, want an empty slice", got["tags"])
	}
	if counts, ok := got["counts"].(map[string]any); !ok || len(counts) != 0 {
		t.Errorf("counts = %#v, want an empty map", got["counts"])
	}
}

// A nullable union is how optional fields are spelled in practically every
// real Avro schema, so both branches matter.
func TestNullableUnionRoundTrips(t *testing.T) {
	const schema = `{"type":"record","name":"U","fields":[
		{"name":"opt","type":["null","string"]}]}`

	t.Run("null branch", func(t *testing.T) {
		got := roundTrip(t, schema, map[string]any{"opt": nil})
		if got["opt"] != nil {
			t.Errorf("opt = %#v, want nil", got["opt"])
		}
	})

	t.Run("value branch", func(t *testing.T) {
		got := roundTrip(t, schema, map[string]any{"opt": map[string]any{"string": "here"}})
		if got["opt"] != "here" {
			t.Errorf("opt = %#v, want \"here\" — a decoded union yields the branch value, "+
				"not hamba's {type: value} encoding wrapper", got["opt"])
		}
	})
}

func TestEnumAndFixedRoundTrip(t *testing.T) {
	const schema = `{"type":"record","name":"EF","fields":[
		{"name":"e","type":{"type":"enum","name":"Colour","symbols":["RED","GREEN","BLUE"]}},
		{"name":"f","type":{"type":"fixed","name":"Hash","size":4}}]}`

	got := roundTrip(t, schema, map[string]any{
		"e": "GREEN",
		"f": [4]byte{1, 2, 3, 4},
	})

	if got["e"] != "GREEN" {
		t.Errorf("enum = %#v, want GREEN", got["e"])
	}
	f, ok := got["f"].([]byte)
	if !ok || len(f) != 4 || f[0] != 1 || f[3] != 4 {
		t.Errorf("fixed = %#v, want 4 bytes 01020304", got["f"])
	}
}

// A recursive schema is legal and appears in real data (linked lists, trees).
// It must terminate on the data, not on the schema.
func TestRecursiveSchemaRoundTrips(t *testing.T) {
	const schema = `{"type":"record","name":"Node","fields":[
		{"name":"v","type":"long"},
		{"name":"next","type":["null","Node"]}]}`

	got := roundTrip(t, schema, map[string]any{
		"v": int64(1),
		"next": map[string]any{"Node": map[string]any{
			"v":    int64(2),
			"next": nil,
		}},
	})

	if got["v"] != int64(1) {
		t.Errorf("v = %#v, want 1", got["v"])
	}
	next, ok := got["next"].(map[string]any)
	if !ok {
		t.Fatalf("next = %#v, want a nested record", got["next"])
	}
	if next["v"] != int64(2) {
		t.Errorf("next.v = %#v, want 2", next["v"])
	}
	if next["next"] != nil {
		t.Errorf("next.next = %#v, want nil", next["next"])
	}
}

// A long array crossing hamba's internal block boundary exercises multi-block
// reading, which single-block tests never reach.
func TestMultiBlockArrayRoundTrips(t *testing.T) {
	const schema = `{"type":"record","name":"B","fields":[
		{"name":"items","type":{"type":"array","items":"long"}}]}`

	const n = 5000
	in := make([]any, n)
	for i := range in {
		in[i] = int64(i)
	}

	got := roundTrip(t, schema, map[string]any{"items": in})

	items, ok := got["items"].([]any)
	if !ok {
		t.Fatalf("items = %T, want []any", got["items"])
	}
	if len(items) != n {
		t.Fatalf("decoded %d items, want %d", len(items), n)
	}
	for i := range items {
		if items[i] != int64(i) {
			t.Fatalf("items[%d] = %#v, want %d", i, items[i], i)
		}
	}
}

func TestDecodeRejectsANilSchema(t *testing.T) {
	if _, err := Decode(nil, []byte{0x00}, DefaultLimits()); err == nil {
		t.Error("Decode(nil schema) succeeded, want an error")
	}
}

// The top level of a Confluent record is always a record: that is what a
// subject registers. A non-record schema has no field names to produce a map
// from, and guessing one would be worse than refusing.
func TestDecodeRequiresARecordAtTheTopLevel(t *testing.T) {
	schema := mustParse(t, `"string"`)
	if _, err := Decode(schema, []byte{0x06, 'a', 'b', 'c'}, DefaultLimits()); err == nil {
		t.Error("Decode accepted a non-record top-level schema, want an error")
	}
}

// Fuzzing is the part that finds what the hand-written cases did not. The
// property is narrow and absolute: never panic, and never hang.
func FuzzDecodeNeverPanics(f *testing.F) {
	schema := mustParse(&testing.T{}, `{"type":"record","name":"R","fields":[
		{"name":"id","type":"long"},
		{"name":"name","type":"string"},
		{"name":"tags","type":{"type":"array","items":"string"}},
		{"name":"meta","type":{"type":"map","values":"long"}},
		{"name":"e","type":{"type":"enum","name":"E","symbols":["A","B"]}},
		{"name":"fx","type":{"type":"fixed","name":"F","size":3}},
		{"name":"opt","type":["null","string"]},
		{"name":"ts","type":{"type":"long","logicalType":"timestamp-micros"}},
		{"name":"dt","type":{"type":"int","logicalType":"date"}},
		{"name":"tod","type":{"type":"int","logicalType":"time-millis"}},
		{"name":"dec","type":{"type":"bytes","logicalType":"decimal","precision":10,"scale":2}},
		{"name":"dur","type":{"type":"fixed","name":"Dur","size":12,"logicalType":"duration"}},
		{"name":"uid","type":{"type":"string","logicalType":"uuid"}}]}`)

	if seed, err := avro.Marshal(schema, map[string]any{
		"id": int64(1), "name": "ada", "tags": []any{"x"},
		"meta": map[string]any{"a": int64(1)}, "e": "A",
		"fx":  [3]byte{1, 2, 3},
		"opt": map[string]any{"string": "s"},
		"ts":  time.Unix(1704067200, 0).UTC(),
		"dt":  time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		"tod": 3 * time.Second,
		"dec": big.NewRat(12345, 100),
		"dur": avro.LogicalDuration{Months: 1, Days: 2, Milliseconds: 3},
		"uid": "9f1b7c62-5f2e-4a1a-8a3f-7c9d0e1b2a34",
	}); err == nil {
		f.Add(seed)
	}
	f.Add([]byte{})
	f.Add([]byte{0xFE, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x01})
	f.Add([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x01})

	f.Fuzz(func(t *testing.T, data []byte) {
		// A panic here fails the test by default, which is the property.
		// Non-termination shows up as a fuzzing timeout.
		_, _ = Decode(schema, data, DefaultLimits())
	})
}
