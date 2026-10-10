package security

import (
	"reflect"
	"testing"
)

// MaskValue and MaskFields are the mask node's rules for data that is not a
// message: a model's logged prediction (internal/ml/monitor).
func TestMaskValueAppliesEachMaskType(t *testing.T) {
	tests := []struct {
		maskType, in, want string
	}{
		{"", "secret", "****"},
		{"all", "secret", "****"},
		{"partial", "4111111111111111", "41****11"},
		{"partial", "abc", "****"},
		{"email", "jane@example.com", "j****@example.com"},
		{"email", "not an email", "****"},
		{"pii", "card 4111111111111111", "card ****-****-****-****"},
	}
	for _, tt := range tests {
		if got := MaskValue(tt.in, tt.maskType); got != tt.want {
			t.Errorf("MaskValue(%q, %q) = %q, want %q", tt.in, tt.maskType, got, tt.want)
		}
	}
}

func TestMaskFieldsMasksNamedPathsAndLeavesTheRest(t *testing.T) {
	data := map[string]any{
		"email":  "jane@example.com",
		"amount": 912.5,
		"card":   map[string]any{"number": "4111111111111111", "brand": "visa"},
		"tags":   []any{"a", "b"},
	}
	MaskFields(data, []string{"email", "card.number", "amount", "missing", "card.brand.deep"}, "partial")

	want := map[string]any{
		"email":  "ja****om",
		"amount": "91****.5", // a number is masked as its text
		"card":   map[string]any{"number": "41****11", "brand": "visa"},
		"tags":   []any{"a", "b"},
	}
	if !reflect.DeepEqual(data, want) {
		t.Errorf("got %#v\nwant %#v", data, want)
	}
}

func TestMaskFieldsStarMasksEveryString(t *testing.T) {
	data := map[string]any{"name": "Jane", "n": 3.0, "nested": map[string]any{"city": "Oslo"}}
	MaskFields(data, []string{"*"}, "all")
	want := map[string]any{"name": "****", "n": 3.0, "nested": map[string]any{"city": "****"}}
	if !reflect.DeepEqual(data, want) {
		t.Errorf("got %#v, want %#v", data, want)
	}
}

func TestMaskFieldsMasksAWholeObjectNamedByPath(t *testing.T) {
	data := map[string]any{"customer": map[string]any{"name": "Jane", "age": 41.0}}
	MaskFields(data, []string{"customer"}, "all")
	want := map[string]any{"customer": map[string]any{"name": "****", "age": 41.0}}
	if !reflect.DeepEqual(data, want) {
		t.Errorf("got %#v, want %#v", data, want)
	}
}
