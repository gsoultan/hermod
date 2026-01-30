package evaluator

import (
	"testing"
)

func TestEvaluateConditions_Regex(t *testing.T) {
	msg := &mockMessage{data: map[string]interface{}{
		"status": "error_404",
		"email":  "test@example.com",
	}}

	tests := []struct {
		name       string
		conditions []map[string]interface{}
		expected   bool
	}{
		{
			"Regex match",
			[]map[string]interface{}{
				{"field": "status", "operator": "regex", "value": "error_.*"},
			},
			true,
		},
		{
			"Regex no match",
			[]map[string]interface{}{
				{"field": "status", "operator": "regex", "value": "success_.*"},
			},
			false,
		},
		{
			"Not Regex match",
			[]map[string]interface{}{
				{"field": "status", "operator": "not_regex", "value": "success_.*"},
			},
			true,
		},
		{
			"Email regex",
			[]map[string]interface{}{
				{"field": "email", "operator": "regex", "value": `^[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,4}$`},
			},
			true,
		},
		{
			"Not contains",
			[]map[string]interface{}{
				{"field": "status", "operator": "not_contains", "value": "ok"},
			},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EvaluateConditions(msg, tt.conditions); got != tt.expected {
				t.Errorf("EvaluateConditions() = %v, want %v", got, tt.expected)
			}
		})
	}
}
