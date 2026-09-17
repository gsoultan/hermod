package core

import (
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// convert drives the node the way the engine does: the value is read back out
// of a message, so it has been through the evaluator's JSON normalization --
// numbers arrive as float64 and typed slices as []any. Tests that need exact Go
// types call the helpers directly instead.
func convert(t *testing.T, in any, cfg map[string]any) (any, error) {
	t.Helper()
	m := message.AcquireMessage()
	m.SetData("in", in)
	cfg["field"] = "in"
	if _, ok := cfg["targetField"]; !ok {
		cfg["targetField"] = "out"
	}
	tr := &DataConversionTransformer{}
	out, err := tr.Transform(t.Context(), m, cfg)
	if err != nil {
		return nil, err
	}
	return out.Data()["out"], nil
}

func TestDataConversion_ToArray(t *testing.T) {
	cases := []struct {
		name string
		in   any
		cfg  map[string]any
		want any
	}{
		{"csv string default separator", "a,b,c", map[string]any{"targetType": "array"}, []any{"a", "b", "c"}},
		{"csv string trims whitespace", "a, b ,c", map[string]any{"targetType": "array"}, []any{"a", "b", "c"}},
		{"custom separator", "a|b", map[string]any{"targetType": "array", "separator": "|"}, []any{"a", "b"}},
		{"multi-char separator", "a::b", map[string]any{"targetType": "array", "separator": "::"}, []any{"a", "b"}},
		{"json array string", `["a","b"]`, map[string]any{"targetType": "array"}, []any{"a", "b"}},
		{"json array of numbers", `[1,2]`, map[string]any{"targetType": "array"}, []any{float64(1), float64(2)}},
		{"empty string is an empty array", "", map[string]any{"targetType": "array"}, []any{}},
		{"scalar number wraps", 42, map[string]any{"targetType": "array"}, []any{float64(42)}},
		{"existing slice normalizes to []any", []string{"a", "b"}, map[string]any{"targetType": "array"}, []any{"a", "b"}},
		{"existing []any passes through", []any{1, "b"}, map[string]any{"targetType": "array"}, []any{float64(1), "b"}},
		{"elementType int coerces", "1,2,3", map[string]any{"targetType": "array", "elementType": "int"}, []any{int64(1), int64(2), int64(3)}},
		{"elementType float coerces", "1.5,2", map[string]any{"targetType": "array", "elementType": "float"}, []any{1.5, float64(2)}},
		{"elementType string coerces", `[1,2]`, map[string]any{"targetType": "array", "elementType": "string"}, []any{"1", "2"}},
		{"elementType bool coerces", "true,false", map[string]any{"targetType": "array", "elementType": "bool"}, []any{true, false}},
		{
			"elementType uuid canonicalizes",
			"6F1A2B3C000000000000000000000001,6f1a2b3c-0000-0000-0000-000000000002",
			map[string]any{"targetType": "array", "elementType": "uuid"},
			[]any{"6f1a2b3c-0000-0000-0000-000000000001", "6f1a2b3c-0000-0000-0000-000000000002"},
		},
		{"single element", "a", map[string]any{"targetType": "array"}, []any{"a"}},
		{"json object is a single element", `{"a":1}`, map[string]any{"targetType": "array"}, []any{map[string]any{"a": float64(1)}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := convert(t, tc.in, tc.cfg)
			if err != nil {
				t.Fatalf("convert: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestDataConversion_ToArray_ElementCoercionFailureIsAnError(t *testing.T) {
	_, err := convert(t, "1,notanint", map[string]any{"targetType": "array", "elementType": "int"})
	if err == nil {
		t.Fatal("want an error for an uncoercible element")
	}
}

func TestDataConversion_ToArray_ElementCoercionRespectsErrorBehavior(t *testing.T) {
	got, err := convert(t, "1,notanint", map[string]any{
		"targetType": "array", "elementType": "int", "errorBehavior": "null"})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if got != nil {
		t.Fatalf("got %#v, want nil", got)
	}
}

func TestDataConversion_ToUUID(t *testing.T) {
	const canonical = "6f1a2b3c-0000-0000-0000-000000000001"
	cases := []struct {
		name string
		in   any
		want any
	}{
		{"canonical passes through", canonical, canonical},
		{"uppercase lowercases", "6F1A2B3C-0000-0000-0000-000000000001", canonical},
		{"unhyphenated", "6f1a2b3c000000000000000000000001", canonical},
		{"braced", "{6f1a2b3c-0000-0000-0000-000000000001}", canonical},
		{"urn prefixed", "urn:uuid:6f1a2b3c-0000-0000-0000-000000000001", canonical},
		{"surrounding whitespace", "  " + canonical + " ", canonical},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := convert(t, tc.in, map[string]any{"targetType": "uuid"})
			if err != nil {
				t.Fatalf("convert: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestDataConversion_ToUUID_InvalidIsAnError(t *testing.T) {
	for _, in := range []any{"not-a-uuid", 42, ""} {
		if _, err := convert(t, in, map[string]any{"targetType": "uuid"}); err == nil {
			t.Fatalf("want an error for %#v", in)
		}
	}
}

// The reverse direction: an array back to a scalar. This used to render Go's
// %v form -- "[a b c]" -- which is not a value any database or downstream
// system accepts.
func TestDataConversion_ArrayToString(t *testing.T) {
	cases := []struct {
		name string
		in   any
		cfg  map[string]any
		want string
	}{
		{"default separator", []any{"a", "b", "c"}, map[string]any{"targetType": "string"}, "a,b,c"},
		{"custom separator", []any{"a", "b"}, map[string]any{"targetType": "string", "separator": "|"}, "a|b"},
		{"separator with space", []any{"a", "b"}, map[string]any{"targetType": "string", "separator": ", "}, "a, b"},
		{"numbers", []any{1, 2.5}, map[string]any{"targetType": "string"}, "1,2.5"},
		{"typed slice", []int{1, 2}, map[string]any{"targetType": "string"}, "1,2"},
		{"empty array", []any{}, map[string]any{"targetType": "string"}, ""},
		{"single element", []any{"a"}, map[string]any{"targetType": "string"}, "a"},
		{"nested value is JSON", []any{map[string]any{"a": 1}}, map[string]any{"targetType": "string"}, `{"a":1}`},
		{"scalar is unchanged", 42, map[string]any{"targetType": "string"}, "42"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := convert(t, tc.in, tc.cfg)
			if err != nil {
				t.Fatalf("convert: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

// Round trip: the two halves of the feature have to be inverses.
func TestDataConversion_RoundTripStringArrayString(t *testing.T) {
	arr, err := convert(t, "u1,u2,u3", map[string]any{"targetType": "array"})
	if err != nil {
		t.Fatal(err)
	}
	back, err := convert(t, arr, map[string]any{"targetType": "string"})
	if err != nil {
		t.Fatal(err)
	}
	if back != "u1,u2,u3" {
		t.Fatalf("round trip gave %#v", back)
	}
}

func TestDataConversion_UnsupportedTargetTypeStillReports(t *testing.T) {
	_, err := convert(t, "x", map[string]any{"targetType": "geography"})
	if err == nil || err.Error() != "unsupported target type: geography" {
		t.Fatalf("err = %v", err)
	}
}

// Type-exact coverage of the helpers, without the message round trip. These are
// the types a database driver hands over directly -- db_lookup writes scanned
// rows into the message as the driver produced them.
func TestDataConversion_toArray_ExactTypes(t *testing.T) {
	tr := &DataConversionTransformer{}
	cases := []struct {
		name string
		in   any
		want []any
	}{
		{"[]int", []int{1, 2}, []any{1, 2}},
		{"[]int64", []int64{1}, []any{int64(1)}},
		{"[]string", []string{"a"}, []any{"a"}},
		{"[]byte is one value", []byte("ab"), []any{[]byte("ab")}},
		{"int scalar", 42, []any{42}},
		{"nil scalar", nil, []any{nil}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tr.toArray(tc.in, "", "")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestDataConversion_toString_ExactTypes(t *testing.T) {
	tr := &DataConversionTransformer{}
	cases := []struct {
		name string
		in   any
		sep  string
		want string
	}{
		{"[]byte renders as text not as a byte list", []byte("ab"), "", "ab"},
		{"[]int joins", []int{1, 2}, "", "1,2"},
		{"[]string joins", []string{"a", "b"}, "|", "a|b"},
		{"nested map is JSON", []any{map[string]any{"a": 1}}, "", `{"a":1}`},
		{"nil element is empty", []any{nil, "b"}, "", ",b"},
		{"plain int unchanged", 7, "", "7"},
		{"nil is empty", nil, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tr.toString(tc.in, tc.sep); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDataConversion_toUUID_ExactTypes(t *testing.T) {
	tr := &DataConversionTransformer{}
	const canonical = "6f1a2b3c-0000-0000-0000-000000000001"
	raw := []byte{0x6f, 0x1a, 0x2b, 0x3c, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}

	for _, tc := range []struct {
		name string
		in   any
	}{
		{"16 raw bytes", raw},
		{"16-byte array", [16]byte(raw)},
		{"uuid.UUID", uuid.MustParse(canonical)},
		{"text bytes", []byte(canonical)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tr.toUUID(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if got != canonical {
				t.Fatalf("got %#v, want %q", got, canonical)
			}
		})
	}

	for _, in := range []any{[]byte{1, 2, 3}, 42, nil, "zzz"} {
		if _, err := tr.toUUID(in); err == nil {
			t.Fatalf("want an error for %#v", in)
		}
	}
}
