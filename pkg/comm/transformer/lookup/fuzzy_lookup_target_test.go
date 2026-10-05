package lookup

import (
	"strings"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
)

// Fuzzy Lookup's field is "Field or expression", and it writes two things: the
// match to field_fuzzy and the score to field_score. With a call for a field
// both names were built from the expression's text.

func fuzzy(t *testing.T, cfg map[string]any) (map[string]any, error) {
	t.Helper()
	m := message.AcquireMessage()
	m.SetData("city", "JAKARTA")
	cfg["options"] = []any{"jakarta", "bandung"}
	out, err := (&FuzzyLookupTransformer{}).Transform(t.Context(), m, cfg)
	if err != nil {
		return nil, err
	}
	return out.Data(), nil
}

func TestFuzzyLookup_ACallWritesBesideItsTargetField(t *testing.T) {
	data, err := fuzzy(t, map[string]any{"field": "lower(source.city)", "targetField": "city_match"})
	if err != nil {
		t.Fatal(err)
	}
	if data["city_match"] != "jakarta" {
		t.Errorf("city_match = %v, want jakarta", data["city_match"])
	}
	// The score has no field of its own to be named after, so it follows the
	// match.
	if data["city_match_score"] != 1.0 {
		t.Errorf("city_match_score = %v, want 1", data["city_match_score"])
	}
	if len(data) != 3 {
		t.Errorf("wrote something besides the match and its score: %v", data)
	}
}

func TestFuzzyLookup_ACallWithNoTargetFieldIsRefused(t *testing.T) {
	data, err := fuzzy(t, map[string]any{"field": "lower(source.city)"})
	if err == nil {
		t.Fatalf("no error; the message now holds %v", data)
	}
	if !strings.Contains(err.Error(), "target field") {
		t.Errorf("error %q should say to set a target field", err)
	}
}

func TestFuzzyLookup_APlainFieldKeepsItsNames(t *testing.T) {
	data, err := fuzzy(t, map[string]any{"field": "city"})
	if err != nil {
		t.Fatal(err)
	}
	if data["city_fuzzy"] != "jakarta" || data["city_score"] != 1.0 || len(data) != 3 {
		t.Errorf("got %v, want city_fuzzy and city_score beside city", data)
	}
}
