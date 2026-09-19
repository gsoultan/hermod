package message

// A payload that is not a JSON object went through three decode attempts
// before anything concluded so: an Unmarshal into a map that had to scan the
// whole body to fail, then TryFixJSON — which copied the entire payload into a
// string just to look at its first character — then an Unmarshal into any.
//
// That is not an exceptional path. A file, CSV, queue or plain-text source
// produces it for every message, and it is also what an already-decoded CDC
// message hits on the first SetData. TryFixJSON alone was 0.83 GB of the
// 4.9 GB a 150k-message engine benchmark allocated.
//
// Skipping the object attempts for a payload that cannot be an object has to
// leave the decoded result identical, which is what TestDecodePayloadFields
// pins.

import (
	"reflect"
	"strings"
	"testing"
)

func TestDecodePayloadFields(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    map[string]any
	}{
		{"object", `{"a":1,"b":"x"}`, map[string]any{"a": float64(1), "b": "x"}},
		{"object with leading space", "  \n\t" + `{"a":1}`, map[string]any{"a": float64(1)}},
		{"object trailing comma", `{"a":1,}`, map[string]any{"a": float64(1)}},
		{"object trailing period", `{"a":1}.`, map[string]any{"a": float64(1)}},
		{"nested object", `{"a":{"b":[1,2]}}`, map[string]any{"a": map[string]any{"b": []any{float64(1), float64(2)}}}},
		{"array", `[1,2,3]`, map[string]any{NonObjectPayloadKey: []any{float64(1), float64(2), float64(3)}}},
		{"array trailing comma", `[1,2,]`, map[string]any{NonObjectPayloadKey: `[1,2,]`}},
		{"quoted string", `"hello"`, map[string]any{NonObjectPayloadKey: "hello"}},
		{"number", `42`, map[string]any{NonObjectPayloadKey: float64(42)}},
		{"bool", `true`, map[string]any{NonObjectPayloadKey: true}},
		// null unmarshals into a map[string]any without error, leaving it nil.
		// It therefore has to keep reaching the object attempt, which is why the
		// fast path below keys off "cannot be an object" rather than "is not {".
		{"null", `null`, nil},
		{"plain text", `hello world`, map[string]any{NonObjectPayloadKey: "hello world"}},
		{"csv line", `a,b,c,1,2,3`, map[string]any{NonObjectPayloadKey: "a,b,c,1,2,3"}},
		{"broken object", `{"a":`, map[string]any{NonObjectPayloadKey: `{"a":`}},
		{"empty", ``, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decodePayloadFields([]byte(tc.payload))
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("decodePayloadFields(%q) = %#v, want %#v", tc.payload, got, tc.want)
			}
		})
	}
}

// TestDecodeNonObjectPayloadCopiesBodyOnce is the point of the change: a body
// that cannot be a JSON object must be recognised from its first byte rather
// than by copying it into a string to look at.
//
// One copy is unavoidable — the decoded result holds the body as a string. Two
// were not: TryFixJSON made the first just to read one character. Measured at
// 64 KiB before the change: 132,350 B/op, 2.02x the body.
func TestDecodeNonObjectPayloadCopiesBodyOnce(t *testing.T) {
	const size = 64 << 10
	body := []byte(strings.Repeat("a", size))

	res := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			decodePayloadFields(body)
		}
	})

	if got := res.AllocedBytesPerOp(); got > size*3/2 {
		t.Errorf("decoding a %d-byte non-JSON body allocates %d B/op (%.2fx the body); want one copy, so under %d",
			size, got, float64(got)/size, size*3/2)
	}
}
