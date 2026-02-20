package evaluator

import (
	"fmt"
	"testing"
)

func TestGetMsgValByPath(t *testing.T) {
	msg := &mockMessage{
		id: "msg-1",
		data: map[string]any{
			"after": map[string]any{
				"id":   123,
				"name": "After Name",
			},
		},
		metadata: map[string]string{
			"source_id": "s1",
		},
		op:    "create",
		table: "users",
	}

	tests := []struct {
		path     string
		expected any
	}{
		{"after.id", 123},
		{"id", "msg-1"}, // should resolve from msg.ID()
		{"after.name", "After Name"},
		{"name", "After Name"},
		{"operation", "create"},
		{"table", "users"},
		{"metadata.source_id", "s1"},
		{"meta.source_id", "s1"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := GetMsgValByPath(msg, tt.path)
			// Simple comparison for test
			if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", tt.expected) {
				t.Errorf("GetMsgValByPath(%s) = %v, want %v", tt.path, got, tt.expected)
			}
		})
	}
}

func TestEvaluateConditions_Regex(t *testing.T) {
	msg := &mockMessage{data: map[string]any{
		"status": "error_404",
		"email":  "test@example.com",
	}}

	tests := []struct {
		name       string
		conditions []map[string]any
		expected   bool
	}{
		{
			"Regex match",
			[]map[string]any{
				{"field": "status", "operator": "regex", "value": "error_.*"},
			},
			true,
		},
		{
			"Regex no match",
			[]map[string]any{
				{"field": "status", "operator": "regex", "value": "success_.*"},
			},
			false,
		},
		{
			"Not Regex match",
			[]map[string]any{
				{"field": "status", "operator": "not_regex", "value": "success_.*"},
			},
			true,
		},
		{
			"Email regex",
			[]map[string]any{
				{"field": "email", "operator": "regex", "value": `^[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,4}$`},
			},
			true,
		},
		{
			"Not contains",
			[]map[string]any{
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

func TestEvaluateConditions_NumericTrimAndMissing(t *testing.T) {
	msg := &mockMessage{data: map[string]any{
		"num":    1,
		"status": "ok",
	}}

	// Numeric comparator should trim surrounding spaces in value
	conds1 := []map[string]any{
		{"field": "num", "operator": ">=", "value": " 1 "},
	}
	if !EvaluateConditions(msg, conds1) {
		t.Errorf("expected numeric comparator with whitespace to pass")
	}

	// Missing field should stringify to empty string and match empty value
	conds2 := []map[string]any{
		{"field": "missing_field", "operator": "=", "value": ""},
	}
	if !EvaluateConditions(msg, conds2) {
		t.Errorf("expected missing field to equal empty string")
	}
}

func TestEvaluateConditions_ValueTemplateResolution(t *testing.T) {
	msg := &mockMessage{data: map[string]any{
		"status":   "ready",
		"expected": "ready",
	}}

	conds := []map[string]any{
		{"field": "status", "operator": "=", "value": "{{.expected}}"},
	}

	if !EvaluateConditions(msg, conds) {
		t.Errorf("expected template value to resolve and match")
	}
}

func TestEvaluateConditions_CDCEnvelopeAliasing(t *testing.T) {
	// Case 1: Root-only payload, condition uses after.id
	msgRoot := &mockMessage{data: map[string]any{
		"id":   1,
		"name": "alice",
	}}

	condsAfter := []map[string]any{
		{"field": "after.id", "operator": "=", "value": "1"},
	}
	if !EvaluateConditions(msgRoot, condsAfter) {
		t.Errorf("expected after.id to resolve to root id when after is absent")
	}

	// Case 2: After-only payload, condition uses root id
	msgAfter := &mockMessage{data: map[string]any{
		"after": map[string]any{
			"id":   2,
			"name": "bob",
		},
	}}

	condsRoot := []map[string]any{
		{"field": "id", "operator": "=", "value": "2"},
	}
	if !EvaluateConditions(msgAfter, condsRoot) {
		t.Errorf("expected root id to resolve to after.id when only after exists")
	}
}

func TestGetMsgValByPath_AfterFallback(t *testing.T) {
	msg := &mockMessage{
		data: map[string]any{
			"id":   1,
			"name": "alice",
		},
		op: "update",
	}

	// For CDC events, if we ask for "after.field", it should find it in the flat data map
	// even if there is no explicit "after" nesting in the data map.
	tests := []struct {
		path     string
		expected any
	}{
		{"after.id", 1},
		{"after.name", "alice"},
		{"name", "alice"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := GetMsgValByPath(msg, tt.path)
			if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", tt.expected) {
				t.Errorf("GetMsgValByPath(%s) = %v, want %v", tt.path, got, tt.expected)
			}
		})
	}
}

func TestEvaluateConditions_CDCMetaFields(t *testing.T) {
	msg := &mockMessage{
		op:     "update",
		table:  "users",
		schema: "public",
		data:   map[string]any{"after": map[string]any{"id": 10}},
	}

	tests := []struct {
		name     string
		conds    []map[string]any
		expected bool
	}{
		{"Operation by name", []map[string]any{{"field": "operation", "operator": "=", "value": "update"}}, true},
		{"Operation alias op", []map[string]any{{"field": "op", "operator": "!=", "value": "delete"}}, true},
		{"Table filter", []map[string]any{{"field": "table", "operator": "=", "value": "users"}}, true},
		{"Schema filter", []map[string]any{{"field": "schema", "operator": "=", "value": "public"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EvaluateConditions(msg, tt.conds); got != tt.expected {
				t.Errorf("EvaluateConditions() = %v, want %v", got, tt.expected)
			}
		})
	}
}
