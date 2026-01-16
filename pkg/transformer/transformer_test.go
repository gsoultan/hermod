package transformer

import (
	"context"
	"encoding/json"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
	"testing"
)

func TestFilterOperationTransformer(t *testing.T) {
	ctx := context.Background()
	trans := &FilterOperationTransformer{
		Operations: map[hermod.Operation]bool{
			hermod.OpCreate: true,
			hermod.OpUpdate: true,
		},
	}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)

	msg.SetOperation(hermod.OpCreate)
	transformed, _ := trans.Transform(ctx, msg)
	if transformed == nil {
		t.Error("Expected OpCreate to be passed through")
	}

	msg.SetOperation(hermod.OpDelete)
	transformed, _ = trans.Transform(ctx, msg)
	if transformed != nil {
		t.Error("Expected OpDelete to be filtered out")
	}
}

func TestFilterDataTransformer(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name      string
		field     string
		operator  string
		value     string
		data      string
		table     string
		operation hermod.Operation
		expected  bool
	}{
		{
			name:  "equals match",
			field: "status", operator: "=", value: "active",
			data:     `{"status":"active","id":1}`,
			expected: true,
		},
		{
			name:  "equals mismatch",
			field: "status", operator: "=", value: "active",
			data:     `{"status":"inactive","id":1}`,
			expected: false,
		},
		{
			name:  "greater than match",
			field: "age", operator: ">", value: "18",
			data:     `{"age":25}`,
			expected: true,
		},
		{
			name:  "greater than mismatch",
			field: "age", operator: ">", value: "18",
			data:     `{"age":15}`,
			expected: false,
		},
		{
			name:  "contains match",
			field: "email", operator: "contains", value: "@example.com",
			data:     `{"email":"user@example.com"}`,
			expected: true,
		},
		{
			name:  "nested field match",
			field: "profile.role", operator: "=", value: "admin",
			data:     `{"profile":{"role":"admin"}}`,
			expected: true,
		},
		{
			name:  "regex match",
			field: "sku", operator: "regex", value: "^ABC-.*",
			data:     `{"sku":"ABC-123"}`,
			expected: true,
		},
		{
			name:  "regex mismatch",
			field: "sku", operator: "regex", value: "^ABC-.*",
			data:     `{"sku":"XYZ-123"}`,
			expected: false,
		},
		{
			name:  "system field table match",
			field: "system.table", operator: "=", value: "users",
			data:     `{"id":1}`,
			table:    "users",
			expected: true,
		},
		{
			name:  "system field table mismatch",
			field: "system.table", operator: "=", value: "users",
			data:     `{"id":1}`,
			table:    "logs",
			expected: false,
		},
		{
			name:  "system field operation match",
			field: "system.operation", operator: "=", value: "create",
			data:      `{"id":1}`,
			operation: hermod.OpCreate,
			expected:  true,
		},
		{
			name:  "exists match",
			field: "tags", operator: "exists",
			data:     `{"tags":["a","b"]}`,
			expected: true,
		},
		{
			name:  "exists mismatch",
			field: "tags", operator: "exists",
			data:     `{"id":1}`,
			expected: false,
		},
		{
			name:  "not exists match",
			field: "deleted_at", operator: "not_exists",
			data:     `{"id":1}`,
			expected: true,
		},
		{
			name:  "not exists mismatch",
			field: "deleted_at", operator: "not_exists",
			data:     `{"deleted_at":"2023-01-01"}`,
			expected: false,
		},
		{
			name:  "missing field with neq",
			field: "role", operator: "!=", value: "admin",
			data:     `{"name":"John"}`,
			expected: true,
		},
		{
			name:  "is null match",
			field: "parent_id", operator: "is_null",
			data:     `{"parent_id":null}`,
			expected: true,
		},
		{
			name:  "is null mismatch (missing)",
			field: "parent_id", operator: "is_null",
			data:     `{"id":1}`,
			expected: false,
		},
		{
			name:  "starts_with match",
			field: "name", operator: "starts_with", value: "John",
			data:     `{"name":"John Doe"}`,
			expected: true,
		},
		{
			name:  "ends_with match",
			field: "email", operator: "ends_with", value: ".com",
			data:     `{"email":"john@example.com"}`,
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conf := map[string]string{
				"field":    tt.field,
				"operator": tt.operator,
				"value":    tt.value,
			}
			tf, err := NewTransformer("filter_data", conf)
			if err != nil {
				t.Fatalf("Failed to create transformer: %v", err)
			}

			msg := message.AcquireMessage()
			defer message.ReleaseMessage(msg)
			msg.SetAfter([]byte(tt.data))
			if tt.table != "" {
				msg.SetTable(tt.table)
			}
			if tt.operation != "" {
				msg.SetOperation(tt.operation)
			}

			transformed, err := tf.Transform(ctx, msg)
			if err != nil {
				t.Fatalf("Transform error: %v", err)
			}

			if (transformed != nil) != tt.expected {
				t.Errorf("expected match %v, got %v for data %s", tt.expected, transformed != nil, tt.data)
			}
		})
	}
}

func TestChainTransformer(t *testing.T) {
	ctx := context.Background()
	t1 := &MappingTransformer{Mapping: map[string]string{"foo": "bar"}, Strict: false}
	t2 := &FilterOperationTransformer{Operations: map[hermod.Operation]bool{hermod.OpCreate: true}}

	chain := NewChain(t1, t2)

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetData("foo", "orig")
	msg.SetOperation(hermod.OpCreate)

	transformed, _ := chain.Transform(ctx, msg)
	if transformed == nil {
		t.Fatal("Chain transformation failed: message is nil")
	}

	after := transformed.Payload()
	var m map[string]interface{}
	json.Unmarshal(after, &m)
	if m["bar"] != "orig" {
		t.Errorf("Chain transformation failed: mapping not applied. Got: %s", string(after))
	}

	msg.SetOperation(hermod.OpDelete)
	transformed, _ = chain.Transform(ctx, msg)
	if transformed != nil {
		t.Error("Chain filter failed")
	}
}

func TestMappingTransformer(t *testing.T) {
	ctx := context.Background()
	trans := &MappingTransformer{
		Mapping: map[string]string{
			"first_name": "firstName",
			"last_name":  "lastName",
			"age":        "userAge",
		},
		Strict: true,
	}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)

	inputJSON := `{"first_name":"John","last_name":"Doe","age":30,"extra":"field"}`
	msg.SetAfter([]byte(inputJSON))

	transformed, err := trans.Transform(ctx, msg)
	if err != nil {
		t.Fatalf("Transform failed: %v", err)
	}

	outputJSON := transformed.After()
	var m map[string]interface{}
	json.Unmarshal(outputJSON, &m)

	if m["firstName"] != "John" {
		t.Errorf("Expected firstName 'John', got '%v'", m["firstName"])
	}
	if m["lastName"] != "Doe" {
		t.Errorf("Expected lastName 'Doe', got '%v'", m["lastName"])
	}
	if m["userAge"] != float64(30) {
		t.Errorf("Expected userAge 30, got '%v'", m["userAge"])
	}
	if _, ok := m["extra"]; ok {
		t.Error("Expected 'extra' field to be filtered out")
	}
}

func TestMappingTransformerRelaxed(t *testing.T) {
	ctx := context.Background()
	trans := &MappingTransformer{
		Mapping: map[string]string{
			"first_name": "firstName",
			"age":        "", // drop age
		},
		Strict: false,
	}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)

	inputJSON := `{"first_name":"John","last_name":"Doe","age":30,"extra":"field"}`
	msg.SetAfter([]byte(inputJSON))

	transformed, err := trans.Transform(ctx, msg)
	if err != nil {
		t.Fatalf("Transform failed: %v", err)
	}

	outputJSON := transformed.After()
	var m map[string]interface{}
	json.Unmarshal(outputJSON, &m)

	if m["firstName"] != "John" {
		t.Errorf("Expected firstName 'John', got '%v'", m["firstName"])
	}
	if m["last_name"] != "Doe" {
		t.Errorf("Expected last_name 'Doe' to be preserved, got '%v'", m["last_name"])
	}
	if _, ok := m["first_name"]; ok {
		t.Error("Expected original 'first_name' field to be removed")
	}
	if _, ok := m["age"]; ok {
		t.Error("Expected 'age' field to be dropped")
	}
	if m["extra"] != "field" {
		t.Errorf("Expected 'extra' to be preserved, got '%v'", m["extra"])
	}
}

func TestTransformerNonCDCFallback(t *testing.T) {
	ctx := context.Background()

	// 1. Test Filter Data with Payload fallback
	filterConf := map[string]string{
		"field":    "status",
		"operator": "=",
		"value":    "active",
	}
	filter, _ := NewTransformer("filter_data", filterConf)

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetPayload([]byte(`{"status":"active"}`))

	transformed, err := filter.Transform(ctx, msg)
	if err != nil || transformed == nil {
		t.Errorf("Filter Data failed to fall back to Payload: %v", err)
	}

	// 2. Test Mapping with Payload fallback
	mappingConf := map[string]string{
		"map.first_name": "firstName",
	}
	mapper, _ := NewTransformer("mapping", mappingConf)

	msg.Reset()
	msg.SetPayload([]byte(`{"first_name":"John"}`))

	transformed, err = mapper.Transform(ctx, msg)
	if err != nil {
		t.Fatalf("Mapping failed: %v", err)
	}

	var m map[string]interface{}
	json.Unmarshal(transformed.Payload(), &m)
	if m["firstName"] != "John" {
		t.Errorf("Mapping failed to fall back to Payload, got: %v", m)
	}

	// 3. Test Advanced with Payload fallback
	advancedConf := map[string]string{
		"column.full_name": "source.name",
	}
	advanced, _ := NewTransformer("advanced", advancedConf)

	msg.Reset()
	msg.SetPayload([]byte(`{"name":"Jane"}`))

	transformed, err = advanced.Transform(ctx, msg)
	if err != nil {
		t.Fatalf("Advanced Mapping failed: %v", err)
	}

	json.Unmarshal(transformed.Payload(), &m)
	if m["full_name"] != "Jane" {
		t.Errorf("Advanced Mapping failed to fall back to Payload, got: %v", m)
	}
}

func TestMappingTransformerNested(t *testing.T) {
	ctx := context.Background()
	trans := &MappingTransformer{
		Mapping: map[string]string{
			"user.id":   "userId",
			"user.name": "userName",
		},
		Strict: true,
	}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)

	inputJSON := `{"user":{"id":123,"name":"John"},"other":"data"}`
	msg.SetAfter([]byte(inputJSON))

	transformed, err := trans.Transform(ctx, msg)
	if err != nil {
		t.Fatalf("Transform failed: %v", err)
	}

	var m map[string]interface{}
	json.Unmarshal(transformed.After(), &m)

	if m["userId"] != float64(123) {
		t.Errorf("Expected userId 123, got %v", m["userId"])
	}
	if m["userName"] != "John" {
		t.Errorf("Expected userName John, got %v", m["userName"])
	}
	if len(m) != 2 {
		t.Errorf("Expected 2 fields in strict mode, got %d", len(m))
	}
}

func TestAdvancedTransformerNestedTarget(t *testing.T) {
	ctx := context.Background()
	trans := &AdvancedTransformer{
		Mapping: map[string]string{
			"user.profile.id":   "source.id",
			"user.profile.name": "source.name",
			"meta.timestamp":    "const.2024-01-01",
		},
		Strict: true,
	}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)

	inputJSON := `{"id":1,"name":"John"}`
	msg.SetAfter([]byte(inputJSON))

	transformed, err := trans.Transform(ctx, msg)
	if err != nil {
		t.Fatalf("Transform failed: %v", err)
	}

	var m map[string]interface{}
	json.Unmarshal(transformed.After(), &m)

	// Verify structure
	user, ok := m["user"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected 'user' object")
	}
	profile, ok := user["profile"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected 'user.profile' object")
	}
	if profile["id"] != float64(1) {
		t.Errorf("Expected profile.id 1, got %v", profile["id"])
	}

	meta, ok := m["meta"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected 'meta' object")
	}
	if meta["timestamp"] != "2024-01-01" {
		t.Errorf("Expected meta.timestamp 2024-01-01, got %v", meta["timestamp"])
	}
}

func TestAdvancedTransformerNestedRelaxed(t *testing.T) {
	ctx := context.Background()
	trans := &AdvancedTransformer{
		Mapping: map[string]string{
			"user.id": "source.id",
		},
		Strict: false,
	}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)

	inputJSON := `{"id":1,"name":"John"}`
	msg.SetAfter([]byte(inputJSON))

	transformed, err := trans.Transform(ctx, msg)
	if err != nil {
		t.Fatalf("Transform failed: %v", err)
	}

	var m map[string]interface{}
	json.Unmarshal(transformed.After(), &m)

	if m["id"] != float64(1) {
		t.Errorf("Expected original id 1, got %v", m["id"])
	}
	if m["name"] != "John" {
		t.Errorf("Expected original name John, got %v", m["name"])
	}

	user, ok := m["user"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected 'user' object created by advanced mapping")
	}
	if user["id"] != float64(1) {
		t.Errorf("Expected user.id 1, got %v", user["id"])
	}
}

func TestAdvancedTransformer(t *testing.T) {
	ctx := context.Background()
	trans := &AdvancedTransformer{
		Mapping: map[string]string{
			"id":         "source.user_id",
			"full_name":  "source.name",
			"city":       "source.address.city",
			"timestamp":  "system.now",
			"app":        "const.Hermod",
			"version":    "const.1.0",
			"is_active":  "const.true",
			"raw_string": "just a string",
		},
		Strict: true,
	}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)

	inputJSON := `{"user_id":123,"name":"John Doe","address":{"city":"New York","zip":"10001"},"internal":"secret"}`
	msg.SetAfter([]byte(inputJSON))

	transformed, err := trans.Transform(ctx, msg)
	if err != nil {
		t.Fatalf("Transform failed: %v", err)
	}

	outputJSON := transformed.After()
	var m map[string]interface{}
	json.Unmarshal(outputJSON, &m)

	if m["id"] != float64(123) {
		t.Errorf("Expected id 123, got '%v'", m["id"])
	}
	if m["full_name"] != "John Doe" {
		t.Errorf("Expected full_name 'John Doe', got '%v'", m["full_name"])
	}
	if m["city"] != "New York" {
		t.Errorf("Expected city 'New York', got '%v'", m["city"])
	}
	if _, ok := m["timestamp"]; !ok {
		t.Error("Expected 'timestamp' field to be present")
	}
	if m["app"] != "Hermod" {
		t.Errorf("Expected app 'Hermod', got '%v'", m["app"])
	}
	if m["version"] != float64(1) && m["version"] != "1.0" {
		// Note: strconv.ParseFloat("1.0", 64) returns 1
		if m["version"] != float64(1) {
			t.Errorf("Expected version 1.0 (float64(1)), got '%v'", m["version"])
		}
	}
	if m["is_active"] != true {
		t.Errorf("Expected is_active true, got '%v'", m["is_active"])
	}
	if m["raw_string"] != "just a string" {
		t.Errorf("Expected raw_string 'just a string', got '%v'", m["raw_string"])
	}
	if _, ok := m["internal"]; ok {
		t.Error("Expected 'internal' field to be filtered out")
	}

	// Test with Before
	msg.Reset()
	msg.SetBefore([]byte(inputJSON))
	transformed, err = trans.Transform(ctx, msg)
	if err != nil {
		t.Fatalf("Transform failed on Before: %v", err)
	}
	outputJSON = transformed.Before()
	json.Unmarshal(outputJSON, &m)
	if m["id"] != float64(123) {
		t.Errorf("Expected id 123 in Before, got '%v'", m["id"])
	}
}

func TestNewTransformerAdvanced(t *testing.T) {
	config := map[string]string{
		"column.id":   "source.id",
		"column.time": "system.now",
	}
	tr, err := NewTransformer("advanced", config)
	if err != nil {
		t.Fatalf("NewTransformer failed: %v", err)
	}

	at, ok := tr.(*AdvancedTransformer)
	if !ok {
		t.Fatal("Expected *AdvancedTransformer")
	}

	if at.Mapping["id"] != "source.id" {
		t.Errorf("Expected mapping id -> source.id, got %s", at.Mapping["id"])
	}
	if at.Mapping["time"] != "system.now" {
		t.Errorf("Expected mapping time -> system.now, got %s", at.Mapping["time"])
	}
}

func TestAdvancedTransformerSystemFields(t *testing.T) {
	ctx := context.Background()
	trans := &AdvancedTransformer{
		Mapping: map[string]string{
			"orig_table": "source.system.table",
			"new_field":  "const.fixed",
		},
		Strict: false,
	}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetTable("my_table")
	msg.SetAfter([]byte(`{"id":1}`))

	transformed, err := trans.Transform(ctx, msg)
	if err != nil {
		t.Fatalf("Transform failed: %v", err)
	}

	var m map[string]interface{}
	json.Unmarshal(transformed.After(), &m)

	if m["orig_table"] != "my_table" {
		t.Errorf("Expected orig_table 'my_table', got '%v'", m["orig_table"])
	}
	if _, exists := m["system.table"]; exists {
		t.Error("system.table leaked into final payload")
	}
	if _, exists := m["__table"]; exists {
		t.Error("__table leaked into final payload")
	}
	if m["id"] != float64(1) {
		t.Errorf("Original field 'id' lost, got '%v'", m["id"])
	}
}
