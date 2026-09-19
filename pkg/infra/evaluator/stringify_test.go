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
		float64(0),
		float64(1),
		float64(1.5),
		float64(-2.25),
		float64(1e21),
		float64(1e-7),
		float64(0.1 + 0.2),
		math.MaxFloat64,
		math.SmallestNonzeroFloat64,
		math.NaN(),
		math.Inf(1),
		math.Inf(-1),
		float32(1.5),
		float32(0.1),
		[]any{1, "a"},
		map[string]any{"k": "v"},
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
