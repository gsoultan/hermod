package transformer

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

func TestStrictMappingClearsMetadata(t *testing.T) {
	ctx := context.Background()

	// 1. Prepare a message that has operation and table (like from a CDC source or misconfigured non-CDC)
	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetID("test-id")
	msg.SetOperation(hermod.OpCreate)
	msg.SetTable("users")
	msg.SetAfter([]byte(`{"name":"John","age":30}`))

	// 2. Configure a STRICT mapping that only maps 'name'
	trans := &MappingTransformer{
		Mapping: map[string]string{
			"name": "full_name",
		},
		Strict: true,
	}

	// 3. Transform
	transformed, err := trans.Transform(ctx, msg)
	if err != nil {
		t.Fatalf("Transform failed: %v", err)
	}

	// 4. Verify that the resulting message has NO operation and NO table
	if transformed.Operation() != "" {
		t.Errorf("Expected empty operation in Strict mode, got %v", transformed.Operation())
	}
	if transformed.Table() != "" {
		t.Errorf("Expected empty table in Strict mode, got %v", transformed.Table())
	}
	if transformed.ID() != "" {
		t.Errorf("Expected empty ID in Strict mode, got %v", transformed.ID())
	}

	// 5. Verify the JSON payload
	var m map[string]interface{}
	json.Unmarshal(transformed.Payload(), &m)
	if m["full_name"] != "John" {
		t.Errorf("Expected full_name John, got %v", m["full_name"])
	}
	if len(m) != 1 {
		t.Errorf("Expected only 1 field in payload, got %d", len(m))
	}

	// 6. Verify MarshalJSON (should NOT include id, operation, table)
	bz, _ := json.Marshal(transformed)
	var finalJSON map[string]interface{}
	json.Unmarshal(bz, &finalJSON)

	if _, ok := finalJSON["id"]; ok {
		t.Error("JSON should not contain 'id'")
	}
	if _, ok := finalJSON["operation"]; ok {
		t.Error("JSON should not contain 'operation'")
	}
	if _, ok := finalJSON["table"]; ok {
		t.Error("JSON should not contain 'table'")
	}
}

func TestStrictMappingPreservesMetadataIfExplicitlyMapped(t *testing.T) {
	ctx := context.Background()

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetID("preserved-id")
	msg.SetOperation(hermod.OpUpdate)
	msg.SetAfter([]byte(`{"name":"John"}`))

	// Explicitly map the system fields
	trans := &MappingTransformer{
		Mapping: map[string]string{
			"name":             "full_name",
			"system.id":        "system.id",
			"system.operation": "system.operation",
		},
		Strict: true,
	}

	transformed, _ := trans.Transform(ctx, msg)

	if transformed.ID() != "preserved-id" {
		t.Errorf("Expected ID preserved-id, got %v", transformed.ID())
	}
	if transformed.Operation() != hermod.OpUpdate {
		t.Errorf("Expected operation update, got %v", transformed.Operation())
	}
}

func TestNonStrictMappingPreservesMetadataAutomatically(t *testing.T) {
	ctx := context.Background()

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetID("auto-id")
	msg.SetOperation(hermod.OpDelete)
	msg.SetAfter([]byte(`{"name":"John"}`))

	// Non-strict mapping
	trans := &MappingTransformer{
		Mapping: map[string]string{
			"name": "full_name",
		},
		Strict: false,
	}

	transformed, _ := trans.Transform(ctx, msg)

	if transformed.ID() != "auto-id" {
		t.Errorf("Expected ID auto-id, got %v", transformed.ID())
	}
	if transformed.Operation() != hermod.OpDelete {
		t.Errorf("Expected operation delete, got %v", transformed.Operation())
	}
}
