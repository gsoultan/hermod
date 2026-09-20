package avrodecode

import (
	"strings"
	"testing"
	"time"

	"github.com/hamba/avro/v2"
)

// These are the abuse cases. They exist first and separately from the
// correctness tests because they are the entire reason this package was
// written rather than a dependency taken.
//
// CVE-2026-46385 / GO-2026-5046-5048: github.com/hamba/avro/v2's array and map
// decoders loop over an attacker-controlled block count without re-checking the
// reader's error state inside the loop body. A producer declares a block of up
// to math.MaxInt64 elements, follows it with a truncated payload, and the
// decoder performs that many no-op iterations before noticing — pinning a CPU
// core until the process is killed.
//
// Every test here bounds wall-clock time, because "returns an error eventually"
// is exactly what the vulnerable implementation also does.

// abuseBudget is generous enough to be stable on a loaded CI machine and still
// many orders of magnitude below what spinning over 2^63 iterations costs.
const abuseBudget = 2 * time.Second

func mustParse(t *testing.T, s string) avro.Schema {
	t.Helper()
	sch, err := avro.Parse(s)
	if err != nil {
		t.Fatalf("parsing schema: %v", err)
	}
	return sch
}

// decodeWithin runs Decode and reports whether it returned inside abuseBudget.
//
// The goroutine is deliberately leaked on timeout rather than waited on: if the
// decoder really is spinning, waiting for it is the hang this test exists to
// catch.
func decodeWithin(t *testing.T, schema avro.Schema, data []byte) (returned bool, err error) {
	t.Helper()

	type result struct {
		val map[string]any
		err error
	}
	done := make(chan result, 1)
	go func() {
		v, err := Decode(schema, data, DefaultLimits())
		done <- result{v, err}
	}()

	select {
	case r := <-done:
		return true, r.err
	case <-time.After(abuseBudget):
		return false, nil
	}
}

// The headline case, in the shape the advisory describes.
func TestHugeArrayBlockCountWithTruncatedBodyFailsImmediately(t *testing.T) {
	schema := mustParse(t, `{"type":"record","name":"R","fields":[
		{"name":"items","type":{"type":"array","items":"string"}}]}`)

	// Zigzag encoding of math.MaxInt64 is 0xFE...0x01 — ten continuation
	// bytes. Then nothing: the body is truncated exactly as the advisory says.
	body := []byte{0xFE, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x01}

	returned, err := decodeWithin(t, schema, body)
	if !returned {
		t.Fatalf("Decode did not return within %v on a block count of math.MaxInt64 — "+
			"this is CVE-2026-46385 reproduced", abuseBudget)
	}
	if err == nil {
		t.Fatal("Decode accepted an array claiming math.MaxInt64 elements, want an error")
	}
}

func TestHugeMapBlockCountWithTruncatedBodyFailsImmediately(t *testing.T) {
	schema := mustParse(t, `{"type":"record","name":"R","fields":[
		{"name":"m","type":{"type":"map","values":"string"}}]}`)

	body := []byte{0xFE, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x01}

	returned, err := decodeWithin(t, schema, body)
	if !returned {
		t.Fatalf("Decode did not return within %v on a map block count of math.MaxInt64", abuseBudget)
	}
	if err == nil {
		t.Fatal("Decode accepted a map claiming math.MaxInt64 entries, want an error")
	}
}

// An array of null is the case a "count must not exceed the bytes remaining"
// check alone does not catch: null occupies zero bytes, so a colossal count is
// arithmetically consistent with an empty body. Only an absolute bound stops
// it, and without one this allocates until the process dies.
func TestHugeArrayOfNullIsRefusedDespiteBeingWellFormed(t *testing.T) {
	schema := mustParse(t, `{"type":"record","name":"R","fields":[
		{"name":"items","type":{"type":"array","items":"null"}}]}`)

	// A count of 2^40 elements, then the terminating zero block.
	body := []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x01, 0x00}

	returned, err := decodeWithin(t, schema, body)
	if !returned {
		t.Fatalf("Decode did not return within %v on an array of 2^40 nulls", abuseBudget)
	}
	if err == nil {
		t.Fatal("Decode accepted an array of 2^40 nulls, want an error — zero-width items make " +
			"the byte-count check vacuous, so the absolute bound is the only defence")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "limit") &&
		!strings.Contains(strings.ToLower(err.Error()), "exceed") {
		t.Errorf("error %q does not identify itself as a limit", err)
	}
}

// A negative block count is legal Avro: it means abs(count) items preceded by a
// byte size, so a reader can skip the block. The absolute value is still
// attacker-controlled, and math.MinInt64 additionally has no positive
// counterpart — negating it overflows back to itself.
func TestNegativeBlockCountCannotOverflowIntoAcceptance(t *testing.T) {
	schema := mustParse(t, `{"type":"record","name":"R","fields":[
		{"name":"items","type":{"type":"array","items":"string"}}]}`)

	// Zigzag encoding of math.MinInt64.
	body := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x01}

	returned, err := decodeWithin(t, schema, body)
	if !returned {
		t.Fatalf("Decode did not return within %v on a block count of math.MinInt64", abuseBudget)
	}
	if err == nil {
		t.Fatal("Decode accepted a block count of math.MinInt64, want an error")
	}
}

// A string or bytes value declares its own length. The same unbounded-length
// problem applies, and here a single varint buys an arbitrary allocation.
func TestHugeStringLengthIsRefused(t *testing.T) {
	schema := mustParse(t, `{"type":"record","name":"R","fields":[{"name":"s","type":"string"}]}`)

	body := []byte{0xFE, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x01}

	returned, err := decodeWithin(t, schema, body)
	if !returned {
		t.Fatalf("Decode did not return within %v on a string length of math.MaxInt64", abuseBudget)
	}
	if err == nil {
		t.Fatal("Decode accepted a string claiming math.MaxInt64 bytes, want an error")
	}
}

// Truncating a valid record at every possible offset must always produce an
// error and must never panic. Index-out-of-range in a decoder is a crash an
// unauthenticated producer triggers at will.
func TestEveryTruncationOfAValidRecordFailsCleanly(t *testing.T) {
	schema := mustParse(t, `{"type":"record","name":"R","fields":[
		{"name":"id","type":"long"},
		{"name":"name","type":"string"},
		{"name":"tags","type":{"type":"array","items":"string"}},
		{"name":"meta","type":{"type":"map","values":"long"}},
		{"name":"opt","type":["null","string"]}]}`)

	full, err := avro.Marshal(schema, map[string]any{
		"id":   int64(9),
		"name": "ada",
		"tags": []any{"x", "y"},
		"meta": map[string]any{"a": int64(1)},
		"opt":  map[string]any{"string": "here"},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	for cut := 0; cut < len(full); cut++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Decode panicked on input truncated to %d/%d bytes: %v", cut, len(full), r)
				}
			}()
			if _, err := Decode(schema, full[:cut], DefaultLimits()); err == nil {
				t.Errorf("Decode accepted input truncated to %d/%d bytes, want an error", cut, len(full))
			}
		}()
	}
}

// Trailing bytes mean the record does not match the schema the id named. A
// decoder that ignores them silently accepts a mismatched schema, which
// corrupts data rather than crashing — the worse outcome of the two.
func TestTrailingBytesAreRejected(t *testing.T) {
	schema := mustParse(t, `{"type":"record","name":"R","fields":[{"name":"id","type":"long"}]}`)

	full, err := avro.Marshal(schema, map[string]any{"id": int64(1)})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := Decode(schema, append(full, 0xFF, 0xFF), DefaultLimits()); err == nil {
		t.Error("Decode accepted a record with trailing bytes, want an error")
	}
}

// A union's branch index selects a schema. An out-of-range index must be
// refused rather than indexing the branch slice.
func TestOutOfRangeUnionIndexIsRefused(t *testing.T) {
	schema := mustParse(t, `{"type":"record","name":"R","fields":[
		{"name":"u","type":["null","string"]}]}`)

	for _, idx := range []byte{0x04, 0x7F} { // branches 2 and 63; only 0 and 1 exist
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Decode panicked on union index byte %#x: %v", idx, r)
				}
			}()
			if _, err := Decode(schema, []byte{idx}, DefaultLimits()); err == nil {
				t.Errorf("Decode accepted union index byte %#x, want an error", idx)
			}
		}()
	}
}

// An enum's symbol index has the same shape as a union's.
func TestOutOfRangeEnumIndexIsRefused(t *testing.T) {
	schema := mustParse(t, `{"type":"record","name":"R","fields":[
		{"name":"e","type":{"type":"enum","name":"E","symbols":["A","B"]}}]}`)

	if _, err := Decode(schema, []byte{0x08}, DefaultLimits()); err == nil {
		t.Error("Decode accepted enum index 4 against a 2-symbol enum, want an error")
	}
}

// A varint with no terminating byte must not read past the buffer, and one
// longer than ten bytes cannot encode a 64-bit value at all.
func TestMalformedVarintIsRefused(t *testing.T) {
	schema := mustParse(t, `{"type":"record","name":"R","fields":[{"name":"id","type":"long"}]}`)

	cases := map[string][]byte{
		"unterminated": {0x80, 0x80, 0x80},
		"overlong":     {0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01},
		"empty":        {},
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Decode panicked on a %s varint: %v", name, r)
				}
			}()
			if _, err := Decode(schema, body, DefaultLimits()); err == nil {
				t.Errorf("Decode accepted a %s varint, want an error", name)
			}
		})
	}
}

// Deeply nested data must hit a depth limit rather than the Go stack, which
// would be an unrecoverable crash rather than a rejected message.
func TestDeepNestingHitsTheDepthLimitNotTheStack(t *testing.T) {
	// A recursive schema: a node holding an optional next node.
	schema := mustParse(t, `{"type":"record","name":"Node","fields":[
		{"name":"next","type":["null","Node"]}]}`)

	// Each level is one byte: union branch 1 (Node). Then branch 0 (null).
	deep := make([]byte, 0, 200001)
	for i := 0; i < 200000; i++ {
		deep = append(deep, 0x02)
	}
	deep = append(deep, 0x00)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Decode panicked (likely stack exhaustion) on 200k nesting levels: %v", r)
		}
	}()

	returned, err := decodeWithin(t, schema, deep)
	if !returned {
		t.Fatalf("Decode did not return within %v on 200k nesting levels", abuseBudget)
	}
	if err == nil {
		t.Fatal("Decode accepted 200k levels of nesting, want a depth-limit error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "depth") {
		t.Errorf("error %q does not identify itself as a depth limit", err)
	}
}

// Many small blocks are individually legal; their sum is the bound that matters.
// Without a running total, an attacker splits a huge collection into chunks that
// each pass the per-block check.
func TestManySmallBlocksStillHitTheCollectionLimit(t *testing.T) {
	schema := mustParse(t, `{"type":"record","name":"R","fields":[
		{"name":"items","type":{"type":"array","items":"null"}}]}`)

	lim := DefaultLimits()
	lim.MaxCollection = 100

	// 60 blocks of 10 nulls each: every block is small, the total is 600.
	var body []byte
	for i := 0; i < 60; i++ {
		body = append(body, 0x14) // zigzag 10
	}
	body = append(body, 0x00)

	if _, err := Decode(schema, body, lim); err == nil {
		t.Error("Decode accepted 600 elements across 60 blocks against a limit of 100 — " +
			"the bound is per-block rather than cumulative")
	}
}
