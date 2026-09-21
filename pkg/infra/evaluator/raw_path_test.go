package evaluator

import (
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
)

// GetValByPath normalises what it returns to the shape a JSON round trip
// produces -- an int becomes a float64, a []byte becomes base64 -- because
// every transformation, condition and mapping downstream is written against
// that shape. TestGetValByPathMatchesJSONRoundTrip pins the two together and
// that contract is deliberate.
//
// It is the wrong shape for exactly one consumer: a value about to be bound as
// a SQL parameter. A float64 carries 53 bits of mantissa, so a bigint key above
// 2^53 arrives at the driver already rounded -- 9007199254740993 becomes
// 9007199254740992 -- and the query matches no row. Measured against PostgreSQL
// 18.4 via pgx: zero rows, no error, straight into db_lookup's miss policy,
// which is the same "the field is null / no data found" symptom that a
// stringified array produces.
//
// Snowflake-style and other 64-bit identifiers are squarely above 2^53, so this
// is not an edge case for the systems Hermod integrates.
func TestGetMsgRawValByPathPreservesAnIntegerTooLargeForAFloat64(t *testing.T) {
	const big = int64(9007199254740993) // 2^53 + 1, the first integer float64 cannot hold

	msg := message.AcquireMessage()
	defer msg.Release()
	msg.SetData("id", big)

	if got := GetMsgValByPath(msg, "id"); got == any(big) {
		t.Fatalf("GetMsgValByPath returned %#v -- this test is meaningless if the "+
			"normalising path no longer loses the value", got)
	}

	got := GetMsgRawValByPath(msg, "id")
	gotInt, ok := got.(int64)
	if !ok {
		t.Fatalf("GetMsgRawValByPath = %#v (%T), want int64", got, got)
	}
	if gotInt != big {
		t.Errorf("GetMsgRawValByPath = %d, want %d -- a key bound at this value "+
			"matches no row and reports no error", gotInt, big)
	}
}

// Everything the raw accessor can answer, it answers with the value the message
// actually holds. Everything else has to keep working exactly as before,
// because this is the only reader of a lookup key.
func TestGetMsgRawValByPathKeepsTheValueTheMessageHolds(t *testing.T) {
	msg := message.AcquireMessage()
	defer msg.Release()
	msg.SetData("i64", int64(7))
	msg.SetData("i", 7)
	msg.SetData("f", 1.5)
	msg.SetData("s", "x")
	msg.SetData("b", true)
	msg.SetData("nested", map[string]any{"k": int64(8)})
	msg.SetData("list", []any{int64(9), "y"})

	tests := []struct {
		path string
		want any
	}{
		{"i64", int64(7)},
		{"i", 7},
		{"f", 1.5},
		{"s", "x"},
		{"b", true},
		{"nested.k", int64(8)},
		{"list.0", int64(9)},
		{"list.1", "y"},
	}
	for _, tc := range tests {
		if got := GetMsgRawValByPath(msg, tc.path); got != tc.want {
			t.Errorf("GetMsgRawValByPath(%q) = %#v (%T), want %#v (%T)",
				tc.path, got, got, tc.want, tc.want)
		}
	}
}

// A path the walk cannot answer must fall through to the full resolver rather
// than become nil: the virtual fields, the before-image and the raw-payload
// fallbacks all live there, and a lookup key that silently stopped resolving
// would trade one silent miss for another.
func TestGetMsgRawValByPathFallsBackToTheFullResolver(t *testing.T) {
	msg := message.AcquireMessage()
	defer msg.Release()
	msg.SetData("id", int64(1))
	msg.SetTable("orders")

	if got := GetMsgRawValByPath(msg, "table"); got != "orders" {
		t.Errorf("GetMsgRawValByPath(table) = %#v, want the virtual field %q", got, "orders")
	}
	if got := GetMsgRawValByPath(msg, "missing"); got != nil {
		t.Errorf("GetMsgRawValByPath(missing) = %#v, want nil", got)
	}
	if got := GetMsgRawValByPath(msg, ""); got != nil {
		t.Errorf("GetMsgRawValByPath(\"\") = %#v, want nil", got)
	}
	if got := GetMsgRawValByPath(nil, "id"); got != nil {
		t.Errorf("GetMsgRawValByPath(nil) = %#v, want nil", got)
	}
	// "$." is a JSONPath root marker, stripped the same way the full resolver
	// strips it -- a keyField of "$.id" used to resolve to nil and skip the
	// lookup entirely.
	if got := GetMsgRawValByPath(msg, "$.id"); got != int64(1) {
		t.Errorf("GetMsgRawValByPath($.id) = %#v, want int64(1)", got)
	}
}
