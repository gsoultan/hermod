package genai

import (
	"strings"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
)

func classify(t *testing.T, f *fakeLLM, extra map[string]any, data map[string]any) (Classification, map[string]any, error) {
	t.Helper()
	msg := message.AcquireMessage()
	for k, v := range data {
		msg.SetData(k, v)
	}
	c, err := Classify(t.Context(), msg, f.config(extra))
	return c, msg.Data(), err
}

var supportLabels = []any{
	map[string]any{"label": "billing", "description": "invoices, charges, refunds"},
	map[string]any{"label": "bug", "description": "something is broken"},
}

func TestClassify_PicksALabelAndWritesFields(t *testing.T) {
	f := newFakeLLM(t, `{"label":"billing","confidence":0.92,"reason":"mentions a charge"}`)
	c, data, err := classify(t, f, map[string]any{"labels": supportLabels, "inputFields": "subject"}, map[string]any{"subject": "Double charged", "ssn": "123-45-6789"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Label != "billing" || c.Confidence != 0.92 {
		t.Fatalf("c = %+v", c)
	}
	if data[DefaultLabelField] != "billing" || data[DefaultConfidenceField] != 0.92 {
		t.Fatalf("data = %v", data)
	}
	schema := f.bodies[0]["response_format"].(map[string]any)["json_schema"].(map[string]any)["schema"].(map[string]any)
	enum := schema["properties"].(map[string]any)["label"].(map[string]any)["enum"].([]any)
	if len(enum) != 2 || enum[0] != "billing" {
		t.Fatalf("label enum = %v", enum)
	}
	if strings.Contains(f.lastUserText(), "123-45") {
		t.Fatal("a field outside inputFields was sent")
	}
	if !strings.Contains(f.bodies[0]["messages"].([]any)[0].(map[string]any)["content"].(string), "invoices, charges") {
		t.Fatal("label descriptions should be in the instructions")
	}
}

func TestClassify_BelowThresholdOrUnknownIsUnsure(t *testing.T) {
	f := newFakeLLM(t, `{"label":"bug","confidence":0.4}`)
	c, _, err := classify(t, f, map[string]any{"labels": "billing,bug", "threshold": "0.7"}, map[string]any{"t": "x"})
	if err != nil || c.Label != UnsureLabel {
		t.Fatalf("c = %+v err = %v", c, err)
	}
	g := newFakeLLM(t, `{"label":"weather","confidence":0.99}`)
	c, _, err = classify(t, g, map[string]any{"labels": "billing,bug"}, map[string]any{"t": "x"})
	if err != nil || c.Label != UnsureLabel {
		t.Fatalf("a label outside the list must be unsure: c = %+v err = %v", c, err)
	}
}

func TestClassify_NeedsLabels(t *testing.T) {
	f := newFakeLLM(t, `{}`)
	if _, _, err := classify(t, f, nil, nil); err == nil {
		t.Fatal("no labels accepted")
	}
}
