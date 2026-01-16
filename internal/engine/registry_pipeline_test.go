package engine

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/message"
)

type mockStorage struct {
	storage.Storage
}

func (m *mockStorage) GetTransformation(ctx context.Context, id string) (storage.Transformation, error) {
	return storage.Transformation{}, nil
}

func TestTransformationPipelineRegistry(t *testing.T) {
	registry := NewRegistry(&mockStorage{})
	ctx := context.Background()

	msg := message.AcquireMessage()
	msg.SetID("1")
	msg.SetTable("users")
	msg.SetOperation(hermod.OpCreate)
	msg.SetAfter([]byte(`{"id": 1, "name": "john", "email": "JOHN@EXAMPLE.COM"}`))

	transformations := []storage.Transformation{
		{
			Type: "advanced",
			Config: map[string]string{
				"column.email": "lower(source.email)",
				"column.name":  "upper(source.name)",
			},
		},
		{
			Type: "filter_data",
			Config: map[string]string{
				"field":    "name",
				"operator": "=",
				"value":    "JOHN",
			},
		},
	}

	results, err := registry.TestTransformationPipeline(ctx, transformations, msg)
	if err != nil {
		t.Fatalf("Failed to test pipeline: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("Expected 2 results, got %d", len(results))
	}

	// First result: transformed
	var firstAfter map[string]interface{}
	if err := json.Unmarshal(results[0].After(), &firstAfter); err != nil {
		t.Fatalf("Failed to unmarshal first result: %v", err)
	}
	if firstAfter["name"] != "JOHN" {
		t.Errorf("Expected name JOHN, got %v", firstAfter["name"])
	}

	// Second result: passed filter
	if results[1] == nil {
		t.Errorf("Expected second result to pass filter, but it was nil")
	}

	// Test with filtering
	transformations[1].Config["value"] = "DOE"
	results, err = registry.TestTransformationPipeline(ctx, transformations, msg)
	if err != nil {
		t.Fatalf("Failed to test pipeline: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("Expected 2 results, got %d", len(results))
	}
	if results[1] != nil {
		t.Errorf("Expected second result to be nil (filtered), but it was not")
	}
}
