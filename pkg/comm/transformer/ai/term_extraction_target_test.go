package ai

import (
	"strings"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
)

func extractTerms(t *testing.T, cfg map[string]any) (map[string]any, error) {
	t.Helper()
	m := message.AcquireMessage()
	m.SetData("note", "  Urgent Refund  ")
	out, err := (&TermExtractionTransformer{}).Transform(t.Context(), m, cfg)
	if err != nil {
		return nil, err
	}
	return out.Data(), nil
}

func TestTermExtraction_ACallWritesToItsTargetField(t *testing.T) {
	data, err := extractTerms(t, map[string]any{"field": "trim(source.note)", "targetField": "note_terms"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := data["note_terms"]; !ok || len(data) != 2 {
		t.Errorf("got %v, want note_terms beside note and nothing else", data)
	}
}

// It wrote the terms to "trim(source.note)_terms", which SetData splits at
// its dot.
func TestTermExtraction_ACallWithNoTargetFieldIsRefused(t *testing.T) {
	data, err := extractTerms(t, map[string]any{"field": "trim(source.note)"})
	if err == nil {
		t.Fatalf("no error; the message now holds %v", data)
	}
	if !strings.Contains(err.Error(), "target field") {
		t.Errorf("error %q should say to set a target field", err)
	}
}

func TestTermExtraction_APlainFieldKeepsItsName(t *testing.T) {
	data, err := extractTerms(t, map[string]any{"field": "note"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := data["note_terms"]; !ok || len(data) != 2 {
		t.Errorf("got %v, want note_terms beside note", data)
	}
}
