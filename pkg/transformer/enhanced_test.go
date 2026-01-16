package transformer

import (
	"context"
	"encoding/json"
	"github.com/user/hermod/pkg/message"
	"testing"
)

func TestAdvancedTransformerEnhanced(t *testing.T) {
	trans := &AdvancedTransformer{
		Mapping: map[string]string{
			"full_name":   "upper(concat(source.first, system.space, source.last))",
			"score_abs":   "abs(source.score)",
			"id_str":      "to_string(source.id)",
			"first_3":     "substring(source.first, const.0, const.3)",
			"email_clean": "trim(lower(source.email))",
		},
		Strict: true,
	}

	data := map[string]interface{}{
		"first": "John",
		"last":  "Doe",
		"score": -42.5,
		"id":    123,
		"email": "  JOHN@Example.COM  ",
	}
	payload, _ := json.Marshal(data)
	msg := message.AcquireMessage()
	msg.SetPayload(payload)

	transformed, err := trans.Transform(context.Background(), msg)
	if err != nil {
		t.Fatalf("Transform failed: %v", err)
	}

	var result map[string]interface{}
	err = json.Unmarshal(transformed.Payload(), &result)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	expected := map[string]interface{}{
		"full_name":   "JOHN DOE",
		"score_abs":   42.5,
		"id_str":      "123",
		"first_3":     "Joh",
		"email_clean": "john@example.com",
	}

	for k, v := range expected {
		if result[k] != v {
			t.Errorf("Expected %s to be %v, got %v", k, v, result[k])
		}
	}
}

func TestValidatorTransformer(t *testing.T) {
	trans := &ValidatorTransformer{
		Rules: []ValidationRule{
			{Field: "email", Type: "regex", Config: "^[a-z0-9._%+-]+@[a-z0-9.-]+\\.[a-z]{2,}$", Severity: "fail"},
			{Field: "age", Type: "not_null", Severity: "fail"},
			{Field: "score", Type: "type", Config: "number", Severity: "fail"},
		},
	}

	tests := []struct {
		name    string
		data    map[string]interface{}
		wantErr bool
	}{
		{
			"valid",
			map[string]interface{}{"email": "test@example.com", "age": 25, "score": 100},
			false,
		},
		{
			"invalid_email",
			map[string]interface{}{"email": "invalid", "age": 25, "score": 100},
			true,
		},
		{
			"missing_age",
			map[string]interface{}{"email": "test@example.com", "score": 100},
			true,
		},
		{
			"invalid_type",
			map[string]interface{}{"email": "test@example.com", "age": 25, "score": "high"},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, _ := json.Marshal(tt.data)
			msg := message.AcquireMessage()
			msg.SetPayload(payload)

			_, err := trans.Transform(context.Background(), msg)
			if (err != nil) != tt.wantErr {
				t.Errorf("Transform() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
