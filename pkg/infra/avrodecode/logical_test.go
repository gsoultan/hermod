package avrodecode

import (
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/hamba/avro/v2"
)

// Logical types annotate an underlying primitive. A decoder that ignores the
// annotation is not wrong about the bits, it is wrong about the *meaning*: a
// timestamp-micros silently arrives as an int64 of microseconds, lands in a
// bigint column, and nobody notices until someone reads a date.
//
// Expected values are written out literally rather than computed the way the
// implementation computes them, so a wrong epoch or a wrong unit fails here
// instead of agreeing with itself.

func decodeOne(t *testing.T, schemaStr string, body []byte) any {
	t.Helper()
	schema := mustParse(t, schemaStr)
	out, err := Decode(schema, body, DefaultLimits())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	return out["v"]
}

func fieldSchema(inner string) string {
	return `{"type":"record","name":"L","fields":[{"name":"v","type":` + inner + `}]}`
}

// date is days since 1970-01-01, encoded as an int.
func TestDateDecodesToATime(t *testing.T) {
	// 19723 days after 1970-01-01. Encoded through the helper rather than
	// written as bytes: a hand-typed varint here decoded to 2021-12-26 and the
	// test blamed the decoder.
	got := decodeOne(t, fieldSchema(`{"type":"int","logicalType":"date"}`),
		zigzag(19723))

	want := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	ts, ok := got.(time.Time)
	if !ok {
		t.Fatalf("date decoded to %T, want time.Time", got)
	}
	if !ts.Equal(want) {
		t.Errorf("date = %s, want %s", ts.Format(time.RFC3339), want.Format(time.RFC3339))
	}
	if ts.Location() != time.UTC {
		t.Errorf("date location = %s, want UTC", ts.Location())
	}
}

func TestDateHandlesThePreEpochSide(t *testing.T) {
	// -1 day: zigzag(-1) = 1 -> 0x02
	got := decodeOne(t, fieldSchema(`{"type":"int","logicalType":"date"}`), zigzag(-1))
	want := time.Date(1969, 12, 31, 0, 0, 0, 0, time.UTC)
	if ts := got.(time.Time); !ts.Equal(want) {
		t.Errorf("date = %s, want %s", ts.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

// timestamp-millis and -micros are both longs since the epoch. Confusing the
// two is a factor-of-1000 error that still produces a plausible date, which is
// exactly the kind of bug that ships.
func TestTimestampUnitsAreNotConfused(t *testing.T) {
	const millis = int64(1704067200123) // 2024-01-01T00:00:00.123Z
	const micros = int64(1704067200123456)

	t.Run("millis", func(t *testing.T) {
		got := decodeOne(t, fieldSchema(`{"type":"long","logicalType":"timestamp-millis"}`),
			zigzag(millis))
		want := time.Date(2024, 1, 1, 0, 0, 0, 123000000, time.UTC)
		ts, ok := got.(time.Time)
		if !ok {
			t.Fatalf("timestamp-millis decoded to %T, want time.Time", got)
		}
		if !ts.Equal(want) {
			t.Errorf("timestamp-millis = %s, want %s",
				ts.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
		}
	})

	t.Run("micros", func(t *testing.T) {
		got := decodeOne(t, fieldSchema(`{"type":"long","logicalType":"timestamp-micros"}`),
			zigzag(micros))
		want := time.Date(2024, 1, 1, 0, 0, 0, 123456000, time.UTC)
		ts, ok := got.(time.Time)
		if !ok {
			t.Fatalf("timestamp-micros decoded to %T, want time.Time", got)
		}
		if !ts.Equal(want) {
			t.Errorf("timestamp-micros = %s, want %s",
				ts.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
		}
	})
}

// local-timestamp carries no zone. It is still a time.Time, because that is
// what a SQL driver wants, but it must not be shifted by any offset — the
// wall-clock reading is the value.
func TestLocalTimestampIsNotShifted(t *testing.T) {
	const millis = int64(1704067200000)

	got := decodeOne(t, fieldSchema(`{"type":"long","logicalType":"local-timestamp-millis"}`),
		zigzag(millis))
	ts, ok := got.(time.Time)
	if !ok {
		t.Fatalf("local-timestamp-millis decoded to %T, want time.Time", got)
	}
	want := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	if !ts.Equal(want) {
		t.Errorf("local-timestamp-millis = %s, want the same wall clock %s",
			ts.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
	}
}

// time-millis / time-micros are a time of day, not an instant. A Duration
// since midnight is the faithful reading; turning it into a time.Time would
// invent a date.
func TestTimeOfDayDecodesToADuration(t *testing.T) {
	t.Run("millis", func(t *testing.T) {
		// 13:45:30.500 = 49530500 ms
		got := decodeOne(t, fieldSchema(`{"type":"int","logicalType":"time-millis"}`),
			zigzag(49530500))
		want := 13*time.Hour + 45*time.Minute + 30*time.Second + 500*time.Millisecond
		d, ok := got.(time.Duration)
		if !ok {
			t.Fatalf("time-millis decoded to %T, want time.Duration", got)
		}
		if d != want {
			t.Errorf("time-millis = %s, want %s", d, want)
		}
	})

	t.Run("micros", func(t *testing.T) {
		got := decodeOne(t, fieldSchema(`{"type":"long","logicalType":"time-micros"}`),
			zigzag(49530500123))
		want := 13*time.Hour + 45*time.Minute + 30*time.Second +
			500*time.Millisecond + 123*time.Microsecond
		d, ok := got.(time.Duration)
		if !ok {
			t.Fatalf("time-micros decoded to %T, want time.Duration", got)
		}
		if d != want {
			t.Errorf("time-micros = %s, want %s", d, want)
		}
	})
}

func TestUUIDStaysAString(t *testing.T) {
	const id = "9f1b7c62-5f2e-4a1a-8a3f-7c9d0e1b2a34"
	body := append(zigzag(int64(len(id))), []byte(id)...)

	got := decodeOne(t, fieldSchema(`{"type":"string","logicalType":"uuid"}`), body)
	if got != id {
		t.Errorf("uuid = %#v, want %q", got, id)
	}
}

// A decimal is a two's-complement big-endian integer scaled by 10^-scale. It
// decodes to a string so the scale survives exactly: 1.10 and 1.1 are
// different values to a NUMERIC column, and a float or a big.Rat loses that.
func TestDecimalDecodesToAnExactString(t *testing.T) {
	tests := []struct {
		name     string
		scale    int
		unscaled []byte
		want     string
	}{
		{"positive", 2, []byte{0x30, 0x39}, "123.45"},  // 12345
		{"negative", 2, []byte{0xCF, 0xC7}, "-123.45"}, // -12345
		{"zero", 2, []byte{0x00}, "0.00"},
		{"scale zero is an integer", 0, []byte{0x2A}, "42"}, // 42
		{"trailing zero is kept", 2, []byte{0x00, 0x6E}, "1.10"},
		{"value smaller than scale pads", 4, []byte{0x01}, "0.0001"},
		{"negative smaller than scale", 4, []byte{0xFF}, "-0.0001"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			schema := fieldSchema(`{"type":"bytes","logicalType":"decimal","precision":10,"scale":` +
				itoa(tc.scale) + `}`)
			body := append(zigzag(int64(len(tc.unscaled))), tc.unscaled...)

			got := decodeOne(t, schema, body)
			if got != tc.want {
				t.Errorf("decimal = %#v, want %q", got, tc.want)
			}
		})
	}
}

// The same logical type also rides on fixed, where the length comes from the
// schema rather than the record.
//
// The size must be large enough for the declared precision — 5 digits needs 3
// bytes. hamba's parser silently drops the logicalType when it is not, which
// reads as "the decoder ignored the annotation" and cost a debugging round
// here. That fall-through is the right behaviour (see the unknown-logical-type
// test), but the schema below is deliberately wide enough to exercise decimal
// rather than the fall-through.
func TestDecimalOnFixedDecodes(t *testing.T) {
	schema := fieldSchema(
		`{"type":"fixed","name":"D","size":4,"logicalType":"decimal","precision":5,"scale":2}`)

	got := decodeOne(t, schema, []byte{0x00, 0x00, 0x30, 0x39})
	if got != "123.45" {
		t.Errorf("decimal on fixed = %#v, want \"123.45\"", got)
	}
}

// big.Int.String is superlinear in the number of digits, so an unscaled value
// bounded only by MaxBytes (16 MiB by default) is a CPU-exhaustion primitive
// even though every byte of it is "valid". The schema's own precision is the
// correct bound: it is operator-supplied and says how large the value can be.
func TestDecimalIsBoundedByItsDeclaredPrecision(t *testing.T) {
	schema := fieldSchema(
		`{"type":"bytes","logicalType":"decimal","precision":5,"scale":2}`)

	// 64 bytes is wildly more than precision 5 can require.
	huge := make([]byte, 64)
	for i := range huge {
		huge[i] = 0x7F
	}
	body := append(zigzag(int64(len(huge))), huge...)

	schemaParsed := mustParse(t, schema)
	_, err := Decode(schemaParsed, body, DefaultLimits())
	if err == nil {
		t.Fatal("Decode accepted 64 unscaled bytes for a precision-5 decimal, want an error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "precision") {
		t.Errorf("error %q does not identify the precision bound", err)
	}
}

// duration is a fixed(12): three little-endian uint32 of months, days and
// milliseconds. It is not a time.Duration — months are not a fixed length of
// time — so it decodes to its three components.
func TestDurationDecodesToItsThreeComponents(t *testing.T) {
	schema := fieldSchema(
		`{"type":"fixed","name":"Dur","size":12,"logicalType":"duration"}`)

	// months=1, days=2, millis=3, each little-endian uint32.
	body := []byte{
		0x01, 0x00, 0x00, 0x00,
		0x02, 0x00, 0x00, 0x00,
		0x03, 0x00, 0x00, 0x00,
	}

	got := decodeOne(t, schema, body)
	d, ok := got.(Duration)
	if !ok {
		t.Fatalf("duration decoded to %T, want avrodecode.Duration", got)
	}
	if d.Months != 1 || d.Days != 2 || d.Milliseconds != 3 {
		t.Errorf("duration = %+v, want {Months:1 Days:2 Milliseconds:3}", d)
	}
}

// A logical type Hermod does not recognise must fall through to the underlying
// primitive rather than failing: the annotation is advisory, and refusing a
// record because of one would break a topic that is otherwise readable.
func TestUnknownLogicalTypeFallsThroughToThePrimitive(t *testing.T) {
	got := decodeOne(t, fieldSchema(`{"type":"long","logicalType":"nonsense-not-a-real-type"}`),
		zigzag(1704067200000))
	if got != int64(1704067200000) {
		t.Errorf("unknown logical type = %#v, want the raw int64", got)
	}
}

// A logical type on the wrong underlying primitive is a malformed schema, and
// the annotation must be ignored rather than misread — reinterpreting a string
// as an epoch would produce a garbage date from valid bytes.
func TestLogicalTypeOnTheWrongPrimitiveIsIgnored(t *testing.T) {
	// date belongs on int; here it is on a string.
	const s = "not-a-date"
	body := append(zigzag(int64(len(s))), []byte(s)...)

	got := decodeOne(t, fieldSchema(`{"type":"string","logicalType":"date"}`), body)
	if got != s {
		t.Errorf("date-on-string = %#v, want the raw string %q", got, s)
	}
}

// Differential check against hamba's encoder for the types it round-trips, so
// the unit interpretations above are not just self-consistent.
func TestLogicalTypesAgreeWithTheEncoder(t *testing.T) {
	const schema = `{"type":"record","name":"LT","fields":[
		{"name":"ts","type":{"type":"long","logicalType":"timestamp-millis"}},
		{"name":"d","type":{"type":"int","logicalType":"date"}},
		{"name":"dec","type":{"type":"bytes","logicalType":"decimal","precision":10,"scale":2}}]}`

	parsed := mustParse(t, schema)
	ts := time.Date(2024, 3, 17, 10, 30, 0, 0, time.UTC)
	date := time.Date(2024, 3, 17, 0, 0, 0, 0, time.UTC)
	dec := big.NewRat(12345, 100)

	encoded, err := avro.Marshal(parsed, map[string]any{"ts": ts, "d": date, "dec": dec})
	if err != nil {
		t.Fatalf("hamba Marshal: %v", err)
	}

	out, err := Decode(parsed, encoded, DefaultLimits())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if got := out["ts"].(time.Time); !got.Equal(ts) {
		t.Errorf("ts = %s, want %s", got.Format(time.RFC3339Nano), ts.Format(time.RFC3339Nano))
	}
	if got := out["d"].(time.Time); !got.Equal(date) {
		t.Errorf("d = %s, want %s", got.Format(time.RFC3339), date.Format(time.RFC3339))
	}
	if got := out["dec"]; got != "123.45" {
		t.Errorf("dec = %#v, want \"123.45\"", got)
	}
}

// zigzag encodes v the way Avro encodes an int or a long, so the tests above
// can state a value rather than a byte pattern.
func zigzag(v int64) []byte {
	u := uint64(v<<1) ^ uint64(v>>63)
	var out []byte
	for u&^0x7F != 0 {
		out = append(out, byte(u&0x7F)|0x80)
		u >>= 7
	}
	return append(out, byte(u))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
