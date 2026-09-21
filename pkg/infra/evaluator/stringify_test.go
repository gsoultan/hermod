package evaluator

// EvaluateConditions stringifies both sides of every condition before the
// operator switch, through fmt.Sprintf("%v", ...) — which goes via reflection
// and allocates, for every condition, on every message, including the numeric
// comparisons that never look at the string.
//
// stringify short-circuits the types a decoded message actually holds. It has
// to produce exactly what %v produced, or a condition starts matching
// different rows: this is a filter, so a formatting difference is a data
// difference.
//
// The one deliberate exception is a finite float. %v renders those with %g,
// which switches to an exponent above 1e6 — so a field holding 1704207845
// compared as "1.704207845e+09" while the wire, the browser and the editor's
// simulator all said 1704207845. Numbers follow JSON instead, and
// TestStringifyNumberMatchesTheWire is the test that says so; what remains here
// is everything else, plus the non-finite floats that have no JSON form.

import (
	"fmt"
	"math"
	"testing"
)

func TestStringifyMatchesSprintf(t *testing.T) {
	values := []any{
		nil,
		"",
		"text",
		"  spaced  ",
		true,
		false,
		0,
		1,
		-1,
		42,
		99,
		100,
		1234567890,
		int8(7), int16(-8), int32(9), int64(-10),
		uint(1), uint8(2), uint16(3), uint32(4), uint64(5),
		math.NaN(),
		math.Inf(1),
		math.Inf(-1),
		[]byte("bytes"),
		struct{ A int }{A: 1},
	}

	for _, v := range values {
		t.Run(fmt.Sprintf("%T/%v", v, v), func(t *testing.T) {
			want := ""
			if v != nil {
				want = fmt.Sprintf("%v", v)
			}
			if got := stringify(v); got != want {
				t.Errorf("stringify(%#v) = %q, fmt = %q", v, got, want)
			}
		})
	}
}

// TestStringifyNonFiniteFloatsKeepTheirSpelling: NaN and the infinities are the
// values json.Marshal refuses outright, so there is no wire form to defer to.
// They keep what %v always gave them rather than acquiring a new spelling.
func TestStringifyNonFiniteFloatsKeepTheirSpelling(t *testing.T) {
	for _, v := range []any{
		math.NaN(), math.Inf(1), math.Inf(-1),
		float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1)),
	} {
		if got, want := stringify(v), fmt.Sprintf("%v", v); got != want {
			t.Errorf("stringify(%v) = %q, fmt = %q", v, got, want)
		}
	}
}

// The point of the change: a numeric comparison must not pay for a string it
// never reads.
func TestNumericConditionDoesNotStringify(t *testing.T) {
	msg := regexCondMsg(t, "amount", "")
	msg.SetData("amount", 1500.0)

	conds := []map[string]any{{"field": "amount", "operator": "gt", "value": 100.0}}
	if !EvaluateConditions(msg, conds) {
		t.Fatal("precondition: 1500 > 100 did not match")
	}

	allocs := testing.AllocsPerRun(200, func() { EvaluateConditions(msg, conds) })
	if allocs > 4 {
		t.Errorf("a numeric condition allocates %v times per message; both sides are still being formatted as strings", allocs)
	}
}
