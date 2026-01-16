package transformer

import (
	"context"
	"encoding/json"
	"github.com/user/hermod/pkg/message"
	"testing"
)

func TestAdvancedTransformerETL(t *testing.T) {
	tr := &AdvancedTransformer{
		Mapping: map[string]string{
			"b64":         "base64_encode(source.name)",
			"url":         "url_encode(const.http://example.com?a=b c)",
			"def_missing": "default(source.missing, const.fallback)",
			"def_exists":  "default(source.name, const.fallback)",
			"now":         "unix_now()",
		},
		Strict: true,
	}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetAfter([]byte(`{"name": "Hermod"}`))

	res, err := tr.Transform(context.Background(), msg)
	if err != nil {
		t.Fatalf("Transform failed: %v", err)
	}

	var data map[string]interface{}
	json.Unmarshal(res.After(), &data)

	if data["b64"] != "SGVybW9k" {
		t.Errorf("Expected SGVybW9k, got %v", data["b64"])
	}
	if data["def_missing"] != "fallback" {
		t.Errorf("Expected fallback, got %v", data["def_missing"])
	}
	if data["def_exists"] != "Hermod" {
		t.Errorf("Expected Hermod, got %v", data["def_exists"])
	}
	if data["now"] == nil {
		t.Error("Expected now to be set")
	}
}

func TestConditionalTransformer(t *testing.T) {
	inner := &AdvancedTransformer{
		Mapping: map[string]string{"status": "const.transformed"},
		Strict:  false,
	}

	tests := []struct {
		name      string
		condition string
		payload   string
		expected  string
	}{
		{
			"Execute when true",
			"eq(source.id, const.123)",
			`{"id": "123", "status": "original"}`,
			"transformed",
		},
		{
			"Skip when false",
			"eq(source.id, const.456)",
			`{"id": "123", "status": "original"}`,
			"original",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cond := &ConditionalTransformer{
				Condition: tt.condition,
				Inner:     inner,
			}

			msg := message.AcquireMessage()
			defer message.ReleaseMessage(msg)
			msg.SetAfter([]byte(tt.payload))

			res, err := cond.Transform(context.Background(), msg)
			if err != nil {
				t.Fatalf("Transform failed: %v", err)
			}

			var data map[string]interface{}
			json.Unmarshal(res.After(), &data)
			if data["status"] != tt.expected {
				t.Errorf("Expected %s, got %v", tt.expected, data["status"])
			}
		})
	}
}
