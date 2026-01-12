package transformer

import (
	"context"
	"encoding/json"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
	"testing"
)

func TestRenameTableTransformer(t *testing.T) {
	ctx := context.Background()
	trans := &RenameTableTransformer{
		OldName: "users",
		NewName: "customers",
	}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetTable("users")

	transformed, err := trans.Transform(ctx, msg)
	if err != nil {
		t.Fatalf("Transform failed: %v", err)
	}

	if transformed.Table() != "customers" {
		t.Errorf("Expected table 'customers', got '%s'", transformed.Table())
	}
}

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
		name     string
		config   FilterDataTransformer
		data     string
		expected bool
	}{
		{
			name:     "equals match",
			config:   FilterDataTransformer{Field: "status", Operator: "=", Value: "active"},
			data:     `{"status":"active","id":1}`,
			expected: true,
		},
		{
			name:     "equals mismatch",
			config:   FilterDataTransformer{Field: "status", Operator: "=", Value: "active"},
			data:     `{"status":"inactive","id":1}`,
			expected: false,
		},
		{
			name:     "greater than match",
			config:   FilterDataTransformer{Field: "age", Operator: ">", Value: "18"},
			data:     `{"age":25}`,
			expected: true,
		},
		{
			name:     "greater than mismatch",
			config:   FilterDataTransformer{Field: "age", Operator: ">", Value: "18"},
			data:     `{"age":15}`,
			expected: false,
		},
		{
			name:     "contains match",
			config:   FilterDataTransformer{Field: "email", Operator: "contains", Value: "@example.com"},
			data:     `{"email":"user@example.com"}`,
			expected: true,
		},
		{
			name:     "nested field match",
			config:   FilterDataTransformer{Field: "profile.role", Operator: "=", Value: "admin"},
			data:     `{"profile":{"role":"admin"}}`,
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := message.AcquireMessage()
			defer message.ReleaseMessage(msg)
			msg.SetAfter([]byte(tt.data))

			transformed, _ := tt.config.Transform(ctx, msg)
			if (transformed != nil) != tt.expected {
				t.Errorf("expected match %v, got %v", tt.expected, transformed != nil)
			}
		})
	}
}

func TestChainTransformer(t *testing.T) {
	ctx := context.Background()
	t1 := &RenameTableTransformer{OldName: "users", NewName: "customers"}
	t2 := &FilterOperationTransformer{Operations: map[hermod.Operation]bool{hermod.OpCreate: true}}

	chain := NewChain(t1, t2)

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetTable("users")
	msg.SetOperation(hermod.OpCreate)

	transformed, _ := chain.Transform(ctx, msg)
	if transformed == nil || transformed.Table() != "customers" {
		t.Error("Chain transformation failed")
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
