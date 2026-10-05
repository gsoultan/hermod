package ai

import (
	"reflect"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
)

// The editor's "Min Word Length" writes minLength and its "Stopwords" writes
// stopWords. The node read minLen and a list built into it, so neither control
// did anything: a node set to six-letter words still returned three-letter ones.

const termNote = "The urgent refund was for an old order"

func termsOf(t *testing.T, cfg map[string]any, prepare bool) []string {
	t.Helper()
	m := message.AcquireMessage()
	m.SetData("note", termNote)
	cfg["field"] = "note"
	tr := &TermExtractionTransformer{}
	if prepare {
		prepared, err := tr.Prepare(cfg)
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		cfg = prepared
	}
	out, err := tr.Transform(t.Context(), m, cfg)
	if err != nil {
		t.Fatal(err)
	}
	terms, ok := out.Data()["note_terms"].([]string)
	if !ok {
		t.Fatalf("note_terms is %T, want []string", out.Data()["note_terms"])
	}
	return terms
}

func TestTermExtraction_SettingsAsTheyAreStored(t *testing.T) {
	cases := []struct {
		name string
		cfg  map[string]any
		want []string
	}{
		{"nothing set: three letters, the built-in stop words", map[string]any{}, []string{"urgent", "refund", "old", "order"}},
		{"minLength, as the editor writes it", map[string]any{"minLength": 5.0}, []string{"urgent", "refund", "order"}},
		{"minLen, the key the node used to read", map[string]any{"minLen": 5.0}, []string{"urgent", "refund", "order"}},
		{"the editor's key wins when both are there", map[string]any{"minLength": 6.0, "minLen": 3.0}, []string{"urgent", "refund"}},
		{"minLength as text", map[string]any{"minLength": "5"}, []string{"urgent", "refund", "order"}},
		{"a length of two reaches shorter words", map[string]any{"minLength": 2.0}, []string{"urgent", "refund", "old", "order"}},
		{"stopWords, as the editor writes them", map[string]any{"stopWords": "urgent,old"}, []string{"refund", "order"}},
		{"stop words in any case, with spaces", map[string]any{"stopWords": " Urgent , OLD "}, []string{"refund", "order"}},
		{"stopWords as a list", map[string]any{"stopWords": []any{"urgent", "old"}}, []string{"refund", "order"}},
		// The editor's label is "Add words to ignore": they are added to the
		// built-in ones, which keep "the", "was" and "for" out as before.
		{"added to the built-in ones, not instead of them", map[string]any{"stopWords": "order", "minLength": 3.0}, []string{"urgent", "refund", "old"}},
		{"a cleared list", map[string]any{"stopWords": ""}, []string{"urgent", "refund", "old", "order"}},
	}
	for _, c := range cases {
		for _, prepare := range []bool{false, true} {
			name := c.name + ", unprepared"
			if prepare {
				name = c.name + ", prepared"
			}
			t.Run(name, func(t *testing.T) {
				cfg := make(map[string]any, len(c.cfg))
				for k, v := range c.cfg {
					cfg[k] = v
				}
				if got := termsOf(t, cfg, prepare); !reflect.DeepEqual(got, c.want) {
					t.Errorf("terms = %v, want %v", got, c.want)
				}
			})
		}
	}
}

// Prepare caches the stop words beside their text. Transform must not read a
// cache made from other words.
func TestTermExtraction_PrepareDoesNotOutliveTheWordsItRead(t *testing.T) {
	tr := &TermExtractionTransformer{}
	cfg, err := tr.Prepare(map[string]any{"field": "note", "stopWords": "urgent"})
	if err != nil {
		t.Fatal(err)
	}
	cfg["stopWords"] = "refund"

	m := message.AcquireMessage()
	m.SetData("note", termNote)
	out, err := tr.Transform(t.Context(), m, cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"urgent", "old", "order"}
	if got := out.Data()["note_terms"]; !reflect.DeepEqual(got, want) {
		t.Errorf("terms = %v, want %v: the stop words were changed after Prepare", got, want)
	}
}
