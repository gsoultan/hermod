package lookup

import (
	"reflect"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
)

// The editor's Options box is a JSON text field, so a node built there holds
// its options as the text `["Jakarta", "Bandung"]`. The node read
// config["options"].([]any), found no list, and returned the record untouched:
// every Fuzzy Lookup made in the editor matched nothing, and stayed green.

// fuzzyWith runs the node the way the Test button does: on the stored config,
// with nothing prepared.
func fuzzyWith(t *testing.T, options any, prepare bool) (map[string]any, error) {
	t.Helper()
	m := message.AcquireMessage()
	m.SetData("city", "jakrta")
	cfg := map[string]any{"field": "city", "threshold": 0.8}
	if options != nil {
		cfg["options"] = options
	}
	tr := &FuzzyLookupTransformer{}
	if prepare {
		// The engine's path: registry_workflow.go prepares a node's config
		// once and hands the result to every Transform.
		prepared, err := tr.Prepare(cfg)
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		cfg = prepared
	}
	out, err := tr.Transform(t.Context(), m, cfg)
	if err != nil {
		return nil, err
	}
	return out.Data(), nil
}

func TestFuzzyLookup_OptionsInEveryShapeTheyAreStoredIn(t *testing.T) {
	shapes := []struct {
		name    string
		options any
	}{
		{"a list, as an API client sends it", []any{"Jakarta", "Bandung"}},
		{"JSON text, as the editor stores it", `["Jakarta", "Bandung"]`},
		{"JSON text the editor has formatted", "[\n  \"Jakarta\",\n  \"Bandung\"\n]"},
		{"a list of strings, as Go code builds it", []string{"Jakarta", "Bandung"}},
	}
	for _, shape := range shapes {
		for _, prepare := range []bool{false, true} {
			name := shape.name + ", unprepared"
			if prepare {
				name = shape.name + ", prepared"
			}
			t.Run(name, func(t *testing.T) {
				data, err := fuzzyWith(t, shape.options, prepare)
				if err != nil {
					t.Fatal(err)
				}
				if data["city_fuzzy"] != "Jakarta" {
					t.Errorf("city_fuzzy = %v, want Jakarta; the record is %v", data["city_fuzzy"], data)
				}
			})
		}
	}
}

// Text that is not a list is a fault in the node. It used to be the same as no
// options at all: a record passed through, and nothing to say why.
func TestFuzzyLookup_OptionsThatAreNotAListAreRefused(t *testing.T) {
	cases := map[string]any{
		"words with commas":  "Jakarta, Bandung",
		"a JSON object":      `{"a": "Jakarta"}`,
		"a JSON string":      `"Jakarta"`,
		"unfinished JSON":    `["Jakarta",`,
		"a number":           42.0,
		"a map from the API": map[string]any{"a": "Jakarta"},
	}
	for name, options := range cases {
		for _, prepare := range []bool{false, true} {
			t.Run(name, func(t *testing.T) {
				data, err := fuzzyWith(t, options, prepare)
				if err == nil {
					t.Fatalf("no error; the record came back as %v", data)
				}
				if !strings.Contains(err.Error(), "options") || !strings.Contains(err.Error(), `["`) {
					t.Errorf("error %q should name the options and show the shape they take", err)
				}
			})
		}
	}
}

// No options is a node nobody has finished, not a fault: the editor adds the
// node before anyone has typed into it.
func TestFuzzyLookup_NoOptionsLeavesTheRecordAlone(t *testing.T) {
	for name, options := range map[string]any{"unset": nil, "empty text": "", "blank text": "  \n", "an empty list": []any{}, "an empty JSON list": "[]"} {
		t.Run(name, func(t *testing.T) {
			data, err := fuzzyWith(t, options, false)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(data, map[string]any{"city": "jakrta"}) {
				t.Errorf("got %v, want the record untouched", data)
			}
		})
	}
}

// Prepare caches the parsed list beside the text. Transform must not read a
// cache made from other options.
func TestFuzzyLookup_PrepareDoesNotOutliveTheOptionsItRead(t *testing.T) {
	tr := &FuzzyLookupTransformer{}
	cfg, err := tr.Prepare(map[string]any{"field": "city", "options": `["Bandung"]`})
	if err != nil {
		t.Fatal(err)
	}
	cfg["options"] = `["Jakarta"]`

	m := message.AcquireMessage()
	m.SetData("city", "jakarta")
	out, err := tr.Transform(t.Context(), m, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := out.Data()["city_fuzzy"]; got != "Jakarta" {
		t.Errorf("city_fuzzy = %v, want Jakarta: the options were changed after Prepare", got)
	}
}
