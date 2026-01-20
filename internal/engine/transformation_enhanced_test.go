package engine

import (
	"encoding/json"
	"testing"

	"github.com/user/hermod/pkg/message"
)

func TestEnhancedTransformations(t *testing.T) {
	registry := NewRegistry(&mockStorage{})

	tests := []struct {
		name     string
		payload  string
		expr     string
		expected interface{}
	}{
		{
			name:     "Trim function",
			payload:  `{"name": "  John Doe  "}`,
			expr:     "trim(source.name)",
			expected: "John Doe",
		},
		{
			name:     "Concat function",
			payload:  `{"first": "John", "last": "Doe"}`,
			expr:     `concat(source.first, " ", source.last)`,
			expected: "John Doe",
		},
		{
			name:     "Substring function",
			payload:  `{"zip": "12345-6789"}`,
			expr:     "substring(source.zip, 0, 5)",
			expected: "12345",
		},
		{
			name:     "Nested functions",
			payload:  `{"name": "  john doe  "}`,
			expr:     "upper(trim(source.name))",
			expected: "JOHN DOE",
		},
		{
			name:     "Coalesce function",
			payload:  `{"first": null, "second": "fallback"}`,
			expr:     "coalesce(source.first, source.second, \"default\")",
			expected: "fallback",
		},
		{
			name:     "Coalesce function default",
			payload:  `{"first": null, "second": null}`,
			expr:     "coalesce(source.first, source.second, \"default\")",
			expected: "default",
		},
		{
			name:     "Math add",
			payload:  `{"a": 10, "b": 20}`,
			expr:     "add(source.a, source.b)",
			expected: 30.0,
		},
		{
			name:     "Math complex",
			payload:  `{"a": 10, "b": 20, "c": 5}`,
			expr:     "mul(add(source.a, source.b), source.c)",
			expected: 150.0,
		},
		{
			name:     "Date format",
			payload:  `{"created_at": "2026-01-19T13:00:00Z"}`,
			expr:     "date_format(source.created_at, \"2006-01-02\")",
			expected: "2026-01-19",
		},
		{
			name:     "Math round",
			payload:  `{"val": 123.456}`,
			expr:     "round(source.val, 2)",
			expected: 123.46,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := message.AcquireMessage()
			msg.SetAfter([]byte(tt.payload))

			result := registry.evaluateAdvancedExpression(msg, tt.expr)

			// Handle numeric comparison as float64
			if expectedFloat, ok := tt.expected.(float64); ok {
				var actualFloat float64
				switch v := result.(type) {
				case int:
					actualFloat = float64(v)
				case int64:
					actualFloat = float64(v)
				case float64:
					actualFloat = v
				default:
					t.Errorf("Expected float-compatible result, got %T", result)
					return
				}
				if actualFloat != expectedFloat {
					t.Errorf("Expected %v, got %v", expectedFloat, actualFloat)
				}
			} else if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestTransformationPipelineEnhanced(t *testing.T) {
	registry := NewRegistry(&mockStorage{})

	msg := message.AcquireMessage()
	msg.SetAfter([]byte(`{"first_name": "John", "last_name": "Doe", "age": 30}`))

	config := map[string]interface{}{
		"column.full_name": `concat(source.first_name, " ", source.last_name)`,
		"column.initials":  `concat(substring(source.first_name, 0, 1), substring(source.last_name, 0, 1))`,
		"column.is_adult":  `add(source.age, 0)`, // Just to test math in pipeline
	}

	res, err := registry.applyTransformation(msg, "advanced", config)
	if err != nil {
		t.Fatalf("Failed to apply transformation: %v", err)
	}

	var data map[string]interface{}
	json.Unmarshal(res.After(), &data)

	if data["full_name"] != "John Doe" {
		t.Errorf("Expected John Doe, got %v", data["full_name"])
	}
	if data["initials"] != "JD" {
		t.Errorf("Expected JD, got %v", data["initials"])
	}
}
