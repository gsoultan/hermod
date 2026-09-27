package core

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

// split() is the one-node spelling of a Data Conversion to Array followed by
// an index, and an operator can reach for either, so both must cut text into
// the same parts. Text that is itself a JSON array is the deliberate
// exception: the conversion reads it as that array, because a jsonb column
// arrives as text on the CDC path, while split() is a string function and
// splits it as written.
func TestSplitFunctionMatchesDataConversionArray(t *testing.T) {
	cases := []struct{ text, sep string }{
		{"a, b ,c", ","},
		{"a|b", "|"},
		{"a::b", "::"},
		{"a,,b", ","},
		{"Ada King Lovelace", " "},
		{"no separator here", ";"},
		{"x,y", ""},
		{"", ","},
		{"   ", ","},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%q on %q", tc.text, tc.sep), func(t *testing.T) {
			converted, err := convert(t, tc.text, map[string]any{"targetType": "array", "separator": tc.sep})
			if err != nil {
				t.Fatalf("data_conversion: %v", err)
			}
			split := evaluator.NewEvaluator().CallFunction("split", []any{tc.text, tc.sep})
			if !reflect.DeepEqual(split, converted) {
				t.Errorf("split() gave %#v; Data Conversion to Array gave %#v", split, converted)
			}
		})
	}
}
