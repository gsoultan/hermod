package evaluator

import (
	"encoding/json"
	"math"
	"testing"
)

// An exact decimal column now reaches the evaluator as a json.Number, so that
// the generic database/sql scan and the pgx-native path agree on the shape of a
// `numeric` column. json.Number is a *named string type*, so it matches no
// `case string` anywhere -- every coercion site has to name it explicitly or it
// silently falls through to the zero value.

func TestToFloat64ReadsAJSONNumber(t *testing.T) {
	for _, tc := range []struct {
		in   json.Number
		want float64
	}{
		{"1200.50", 1200.5},
		{"-0.125", -0.125},
		{"0", 0},
		{"1e3", 1000},
	} {
		got, ok := ToFloat64(tc.in)
		if !ok {
			t.Errorf("ToFloat64(json.Number(%q)) reported failure", tc.in)
			continue
		}
		if got != tc.want {
			t.Errorf("ToFloat64(json.Number(%q)) = %v, want %v", tc.in, got, tc.want)
		}
	}

	// A json.Number can hold anything; a condition must not read garbage as 0.
	if _, ok := ToFloat64(json.Number("not a number")); ok {
		t.Error("ToFloat64 accepted a json.Number that is not a number")
	}
}

func TestToInt64ReadsAJSONNumber(t *testing.T) {
	for _, tc := range []struct {
		in   json.Number
		want int64
	}{
		{"42", 42},
		{"-7", -7},
		// Truncation, matching what ToInt64 already does for a float64.
		{"1200.50", 1200},
	} {
		got, ok := ToInt64(tc.in)
		if !ok {
			t.Errorf("ToInt64(json.Number(%q)) reported failure", tc.in)
			continue
		}
		if got != tc.want {
			t.Errorf("ToInt64(json.Number(%q)) = %d, want %d", tc.in, got, tc.want)
		}
	}

	// The whole reason numeric is carried as text: an id past float64's exact
	// range must survive the read that a float64 would have rounded.
	const big = "9007199254740993"
	got, ok := ToInt64(json.Number(big))
	if !ok || got != 9007199254740993 {
		t.Errorf("ToInt64(json.Number(%q)) = %d, %v; want 9007199254740993, true -- "+
			"parsing through float64 would give ...992", big, got, ok)
	}
}

// stringify decides what a condition compares against, so its rendering is a
// data decision. A json.Number's own text is exactly the wire form, which is
// the answer %v would give for the string but not the one it gives for a
// float64 -- 1200.50 rather than 1200.5.
func TestStringifyRendersAJSONNumberAsTheWireForm(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"1200.50", "1200.50"},
		{"1.00000000000000000001", "1.00000000000000000001"},
		{"1704207845", "1704207845"},
	} {
		if got := stringify(json.Number(tc.in)); got != tc.want {
			t.Errorf("stringify(json.Number(%q)) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// normalizeToJSONShape must answer exactly what the JSON round trip answers, or
// the fast walk and the round trip disagree. json.Marshal writes a json.Number
// as a bare number and json.Unmarshal reads a bare number back as a float64, so
// the normalised shape is a float64 -- the same shape the pgx-native path
// already resolves to today.
func TestNormalizeToJSONShapeTurnsAJSONNumberIntoAFloat(t *testing.T) {
	got, ok := normalizeToJSONShape(json.Number("1200.50"))
	if !ok {
		t.Fatal("normalizeToJSONShape refused a valid json.Number")
	}
	f, isFloat := got.(float64)
	if !isFloat {
		t.Fatalf("normalizeToJSONShape gave %#v (%T), want float64", got, got)
	}
	if f != 1200.5 {
		t.Errorf("got %v, want 1200.5", f)
	}

	// Anything it cannot reproduce exactly must bail out to the real round
	// trip rather than guess.
	if _, ok := normalizeToJSONShape(json.Number("not a number")); ok {
		t.Error("normalizeToJSONShape claimed it could normalise a non-numeric json.Number")
	}

	// A value past float64's range normalises to +Inf rather than bailing, and
	// that is correct: the round trip marshals 1e400 to a bare number gjson
	// also reads as +Inf, so the two agree. Asserting a bail-out here would
	// pin the fast path to something the oracle does not do.
	over, ok := normalizeToJSONShape(json.Number("1e400"))
	if !ok || over != any(math.Inf(1)) {
		t.Errorf("normalizeToJSONShape(1e400) = %#v, %v; want +Inf, true (parity with the round trip)", over, ok)
	}
}

// One json.Number that is not a number makes json.Marshal fail for the whole
// map, so every field beside it becomes unreadable through the round trip --
// not just the bad one.
//
// This is why sqlutil.DecodeNumericText validates before constructing one:
// PostgreSQL numeric accepts NaN and Infinity, and a single such cell would
// otherwise take down every other column in the row.
//
// The fast walk and the round trip disagree here, and that divergence is
// PRE-EXISTING rather than anything json.Number introduced: a math.NaN()
// sibling does exactly the same thing. Closing it would mean checking every
// other value in the row on each read, which is the O(row) cost the fast path
// exists to avoid. Documented rather than asserted as parity.
func TestAnInvalidJSONNumberPoisonsTheWholeRow(t *testing.T) {
	row := map[string]any{
		"notanum": json.Number("not a number"),
		"beside":  "an ordinary field",
	}

	if _, err := json.Marshal(row); err == nil {
		t.Fatal("marshalling a row holding an invalid json.Number succeeded; " +
			"the hazard DecodeNumericText guards against is gone")
	}

	// Same shape with a NaN, to keep it honest that this is not about
	// json.Number.
	nanRow := map[string]any{"nan": math.NaN(), "beside": "an ordinary field"}
	if _, err := json.Marshal(nanRow); err == nil {
		t.Error("marshalling a row holding NaN succeeded; the comparison this test " +
			"rests on no longer holds")
	}
}

// A json.Number is NOT JSON-native, and saying otherwise would be the fast-path
// drift this guard exists to prevent: marshalling and unmarshalling a
// json.Number is not the identity -- it comes back a float64. Reporting false
// costs the fast path on a row carrying a decimal, and is the price of the two
// paths agreeing.
func TestAJSONNumberIsNotJSONNative(t *testing.T) {
	if isJSONNativeValue(json.Number("1200.50")) {
		t.Error("isJSONNativeValue(json.Number) = true, but a round trip turns it into a float64")
	}
	if isJSONNativeMap(map[string]any{"ok": "text", "amount": json.Number("1200.50")}) {
		t.Error("a map holding a json.Number reported itself JSON-native")
	}
}

// The end-to-end property the change exists for: a decimal survives the message
// as the digits the database sent, and serialises as a bare JSON number rather
// than a quoted string.
func TestAJSONNumberSerialisesAsAnExactBareNumber(t *testing.T) {
	b, err := json.Marshal(map[string]any{"amount": json.Number("1.00000000000000000001")})
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	const want = `{"amount":1.00000000000000000001}`
	if string(b) != want {
		t.Errorf("got %s, want %s", b, want)
	}

	// And the same value through a float64 is what we are avoiding.
	f, _ := ToFloat64(json.Number("1.00000000000000000001"))
	if f != 1 || math.Abs(f-1) > 0 {
		t.Logf("for the record, float64 gives %v", f)
	}
}

// ToTime already reads a unix timestamp from an int64, an int and a float64.
// A json.Number is the same number, so a `numeric` column holding a timestamp
// must not be the one numeric type that silently fails to convert.
func TestToTimeReadsAJSONNumber(t *testing.T) {
	got, ok := ToTime(json.Number("1704207845"))
	if !ok {
		t.Fatal("ToTime(json.Number) reported failure; a numeric unix timestamp is unreadable")
	}
	if want, _ := ToTime(int64(1704207845)); !got.Equal(want) {
		t.Errorf("ToTime(json.Number) = %v, want %v -- it must agree with the int64 branch", got, want)
	}
}
