package evaluator

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// numberMsg builds a message holding one field, the way a decoded row does.
func numberMsg(field string, v any) *mockMessage {
	return &mockMessage{data: map[string]any{field: v}}
}

// TestStringifyNumberMatchesTheWire is the invariant a condition needs.
//
// Every read path normalises a number to float64 -- GetValByPath reproduces
// what a JSON round trip would produce, deliberately, because every
// transformation and mapping downstream is written against that shape. The API
// then hands the browser that same number as JSON, and the sample panel shows
// the user what JSON.parse gives back.
//
// So the string a condition compares against has to be the string the user was
// shown. Formatting it with %v instead made an integer wide enough to matter
// come out in scientific notation -- 1704207845 as "1.704207845e+09" -- while
// the wire, the browser, and the editor's own simulator all said 1704207845.
// Nothing below 1e6 drifted, so IDs and timestamps broke while the test data
// did not.
func TestStringifyNumberMatchesTheWire(t *testing.T) {
	for _, v := range []float64{
		0, 1, 99, 1000, 100000,
		1000000,          // first magnitude %v renders as 1e+06
		12345678,         // an order id
		1704207845,       // a unix timestamp
		1704207845123,    // a millisecond timestamp
		9007199254740992, // the largest exactly representable integer
		0.1, 100.5, 123456.789,
		12345678.99, // an amount %v renders as 1.234567899e+07
		-1704207845,
		1e21, // beyond this JSON itself uses an exponent
		1e-7, // and below this too
		1e-6,
		0.1 + 0.2, // the classic float that is not what it reads as
		math.MaxFloat64,
		math.SmallestNonzeroFloat64,
	} {
		wire, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("json.Marshal(%v): %v", v, err)
		}
		if got, want := stringify(v), string(wire); got != want {
			t.Errorf("stringify(%v) = %q, the wire says %q", v, got, want)
		}
	}

	// A float32 is rendered at its own width, the way encoding/json does it,
	// so widening to float64 does not invent digits: 0.1 stays "0.1".
	for _, v := range []float32{0, 1, 1.5, 0.1, 1704207845, 1e21, 1e-7} {
		wire, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("json.Marshal(float32 %v): %v", v, err)
		}
		if got, want := stringify(v), string(wire); got != want {
			t.Errorf("stringify(float32 %v) = %q, the wire says %q", v, got, want)
		}
	}
}

// TestConditionMatchesAWideInteger drives the whole operator surface over a
// field wide enough to have been reformatted, because `=` was not the only
// casualty: contains and regex read the same string.
func TestConditionMatchesAWideInteger(t *testing.T) {
	const id = 1704207845

	for _, tc := range []struct {
		op    string
		value string
		want  bool
	}{
		{"=", "1704207845", true},
		{"=", "1704207846", false},
		{"!=", "1704207845", false},
		{"!=", "1704207846", true},
		{"contains", "042078", true},
		{"contains", "e+09", false},
		{"not_contains", "042078", false},
		{"regex", "^1704", true},
		{"regex", `^\d+$`, true},
		{"not_regex", "^1704", false},
		{">", "1704207844", true},
		{"<", "1704207846", true},
	} {
		t.Run(tc.op+"/"+tc.value, func(t *testing.T) {
			got := EvaluateConditions(numberMsg("id", float64(id)),
				[]map[string]any{{"field": "id", "operator": tc.op, "value": tc.value}})
			if got != tc.want {
				t.Errorf("id %d %s %q = %v, want %v", id, tc.op, tc.value, got, tc.want)
			}
		})
	}
}

// TestEqualityOnANumberIsNumeric: `>` compared a numeric field as a number
// while `=` compared it as text, so one case list ran under two type regimes.
// A user typing a money amount as 100.00 got "not equal" from a field holding
// 100.5's neighbour 100.0 -- the same number, written the way money is written.
//
// The rule is narrow on purpose: exact text equality still wins first, so no
// comparison that matched before stops matching, and the numeric path is only
// taken when the *field* is genuinely a number. A string field keeps string
// semantics, so an identifier like "007" does not start equalling "7".
func TestEqualityOnANumberIsNumeric(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field any
		value string
		eq    bool
	}{
		{"same number, trailing zero", 100.0, "100.00", true},
		{"same number, decimal zero", 100.5, "100.50", true},
		{"same number, leading zero", 100.0, "0100", true},
		{"same number, plus sign", 100.0, "+100", true},
		{"same number, exponent", 100.0, "1e2", true},
		{"same text", 100.0, "100", true},
		{"different number", 100.0, "101", false},
		{"not a number", 100.0, "abc", false},
		{"string field keeps string equality", "007", "7", false},
		{"string field exact still matches", "007", "007", true},
		{"string field numeric text", "100.0", "100.00", false},
		{"bool field is not a number", true, "1", false},
		{"empty value is not zero", 0.0, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eq := EvaluateConditions(numberMsg("f", tc.field),
				[]map[string]any{{"field": "f", "operator": "=", "value": tc.value}})
			if eq != tc.eq {
				t.Errorf("%#v = %q gave %v, want %v", tc.field, tc.value, eq, tc.eq)
			}

			// != must be the exact negation, or a config can satisfy both.
			ne := EvaluateConditions(numberMsg("f", tc.field),
				[]map[string]any{{"field": "f", "operator": "!=", "value": tc.value}})
			if ne == eq {
				t.Errorf("%#v: = and != both returned %v for %q", tc.field, eq, tc.value)
			}
		})
	}
}

// TestConditionOperatorsAreAllReachable: every operator the editor offers must
// be understood here, or a user picks one from the dropdown and gets a silent
// false. The list is FilterEditor.tsx's and SwitchConfig.tsx's, plus the
// aliases stored configs use.
func TestConditionOperatorsAreAllReachable(t *testing.T) {
	offered := []string{
		"=", "!=", ">", ">=", "<", "<=",
		"contains", "not_contains", "regex", "not_regex",
		"eq", "neq", "gt", "gte", "lt", "lte",
	}

	for _, op := range offered {
		t.Run(op, func(t *testing.T) {
			// Pick a field and value that make the operator true, so a
			// blanket `return false` cannot pass.
			field, value := any("abc"), "abc"
			switch op {
			case "!=", "neq", "not_contains", "not_regex":
				value = "zzz"
			case ">", "gt", ">=", "gte":
				field, value = 10.0, "5"
			case "<", "lt", "<=", "lte":
				field, value = 5.0, "10"
			case "regex":
				value = "^a"
			}
			if !EvaluateConditions(numberMsg("f", field),
				[]map[string]any{{"field": "f", "operator": op, "value": value}}) {
				t.Errorf("operator %q did not match %#v against %q; it is offered in the editor", op, field, value)
			}
		})
	}
}

// A []byte field reaches a condition as base64, because that is what a JSON
// round trip makes of it and what the API hands the browser. This is not a bug
// to fix here -- changing it would put the engine and the editor's simulator
// into disagreement -- but it is surprising enough to pin, so the next person
// reads this instead of rediscovering it.
func TestByteSliceFieldComparesAsBase64(t *testing.T) {
	got := stringify(EvaluateField(numberMsg("f", []byte("active")), "f"))
	if !strings.EqualFold(got, "YWN0aXZl") {
		t.Fatalf("a []byte field stringified to %q; the documented shape is base64", got)
	}
}

// TestCompositeFieldRendersAsJSON: an array or object field is searched as the
// JSON the sample panel shows, not as Go's %v syntax. `contains` against a
// jsonb column is how you filter one without a path, and under `map[a:1 b:2]`
// there was no string a user could type that would match.
func TestCompositeFieldRendersAsJSON(t *testing.T) {
	for _, tc := range []struct {
		field any
		want  string
	}{
		{[]any{1.0, 2.0}, "[1,2]"},
		{[]any{}, "[]"},
		{[]any{"a", "b"}, `["a","b"]`},
		{map[string]any{"a": 1.0}, `{"a":1}`},
		// Keys come out sorted, so the same object renders the same way every
		// time and the browser's JSON.stringify can be made to agree.
		{map[string]any{"b": 2.0, "a": 1.0}, `{"a":1,"b":2}`},
		{map[string]any{"nested": map[string]any{"z": 1.0, "y": 2.0}}, `{"nested":{"y":2,"z":1}}`},
		{map[string]any{}, "{}"},
		// The numbers inside follow the same wire rule as a scalar field.
		{map[string]any{"id": 1704207845.0}, `{"id":1704207845}`},
	} {
		if got := stringify(tc.field); got != tc.want {
			t.Errorf("stringify(%#v) = %q, want %q", tc.field, got, tc.want)
		}
	}

	// And the operators read that rendering.
	m := numberMsg("payload", map[string]any{"status": "ok", "amount": 1704207845.0})
	for _, tc := range []struct {
		op    string
		value string
		want  bool
	}{
		{"contains", `"status":"ok"`, true},
		{"contains", `"amount":1704207845`, true},
		{"contains", "map[", false},
		{"regex", `"status":"ok"`, true},
	} {
		if got := EvaluateConditions(m, []map[string]any{
			{"field": "payload", "operator": tc.op, "value": tc.value},
		}); got != tc.want {
			t.Errorf("payload %s %q = %v, want %v", tc.op, tc.value, got, tc.want)
		}
	}
}

// TestDateFieldComparesChronologically mirrors the date block in
// ui/src/__tests__/matchesConditionParity.test.ts. The ISO formats order
// correctly as text, so both sides agree without either parsing anything;
// RFC1123 leads with the weekday and does not, so the engine parses it and the
// browser's twin had to learn to as well.
func TestDateFieldComparesChronologically(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field string
		value string
		want  bool
	}{
		{"rfc3339", "2024-12-06T15:04:05Z", "2024-01-02T15:04:05Z", true},
		{"sql timestamp", "2024-01-02 15:04:05", "2024-01-02 09:00:00", true},
		{"date only", "2024-01-02", "2024-12-06", false},
		{"rfc1123 later month, earlier weekday", "Fri, 06 Dec 2024 15:04:05 GMT", "Mon, 02 Jan 2024 15:04:05 GMT", true},
		{"rfc1123 reversed", "Mon, 02 Jan 2024 15:04:05 GMT", "Fri, 06 Dec 2024 15:04:05 GMT", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := EvaluateConditions(numberMsg("at", tc.field),
				[]map[string]any{{"field": "at", "operator": ">", "value": tc.value}})
			if got != tc.want {
				t.Errorf("%q > %q = %v, want %v", tc.field, tc.value, got, tc.want)
			}
		})
	}
}
