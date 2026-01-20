package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/message"
)

type policyMockStorage struct {
	storage.Storage
}

func (m *policyMockStorage) GetSource(ctx context.Context, id string) (storage.Source, error) {
	if id == "non-existent" {
		return storage.Source{}, fmt.Errorf("source not found")
	}
	return storage.Source{ID: id, Type: "sqlite"}, nil
}

func TestMappingEnhanced(t *testing.T) {
	registry := NewRegistry(&policyMockStorage{})

	t.Run("Range mapping", func(t *testing.T) {
		msg := message.AcquireMessage()
		msg.SetAfter([]byte(`{"age": 25}`))

		config := map[string]interface{}{
			"field":       "age",
			"mappingType": "range",
			"mapping":     `{"0-18": "child", "19-65": "adult", "66+": "senior"}`,
		}

		res, err := registry.applyTransformation(msg, "mapping", config)
		if err != nil {
			t.Fatalf("Failed: %v", err)
		}

		var data map[string]interface{}
		json.Unmarshal(res.After(), &data)
		if data["age"] != "adult" {
			t.Errorf("Expected adult, got %v", data["age"])
		}
	})

	t.Run("Regex mapping", func(t *testing.T) {
		msg := message.AcquireMessage()
		msg.SetAfter([]byte(`{"status": "error_critical"}`))

		config := map[string]interface{}{
			"field":       "status",
			"mappingType": "regex",
			"mapping":     `{"^error_.*": "failed", "ok": "success"}`,
		}

		res, err := registry.applyTransformation(msg, "mapping", config)
		if err != nil {
			t.Fatalf("Failed: %v", err)
		}

		var data map[string]interface{}
		json.Unmarshal(res.After(), &data)
		if data["status"] != "failed" {
			t.Errorf("Expected failed, got %v", data["status"])
		}
	})
}

func TestErrorPolicies(t *testing.T) {
	registry := NewRegistry(&policyMockStorage{})

	t.Run("Fail policy (default)", func(t *testing.T) {
		msg := message.AcquireMessage()
		msg.SetAfter([]byte(`{"id": 1}`))

		// db_lookup will fail because source doesn't exist
		config := map[string]interface{}{
			"sourceId":    "non-existent",
			"table":       "users",
			"keyColumn":   "id",
			"keyField":    "id",
			"targetField": "name",
		}

		_, err := registry.applyTransformation(msg, "db_lookup", config)
		if err == nil {
			t.Error("Expected error for non-existent source, got nil")
		}
	})

	t.Run("Continue policy", func(t *testing.T) {
		msg := message.AcquireMessage()
		msg.SetAfter([]byte(`{"id": 1}`))

		config := map[string]interface{}{
			"sourceId":    "non-existent",
			"table":       "users",
			"keyColumn":   "id",
			"keyField":    "id",
			"targetField": "name",
			"onError":     "continue",
			"statusField": "_status",
		}

		res, err := registry.applyTransformation(msg, "db_lookup", config)
		if err != nil {
			t.Fatalf("Expected no error due to continue policy, got %v", err)
		}

		var data map[string]interface{}
		json.Unmarshal(res.After(), &data)
		if data["_status"] != "error" {
			t.Errorf("Expected _status to be error, got %v", data["_status"])
		}
		if data["_status_error"] == "" {
			t.Error("Expected _status_error to be populated")
		}
	})

	t.Run("Drop policy", func(t *testing.T) {
		msg := message.AcquireMessage()
		msg.SetAfter([]byte(`{"id": 1}`))

		config := map[string]interface{}{
			"sourceId":    "non-existent",
			"table":       "users",
			"keyColumn":   "id",
			"keyField":    "id",
			"targetField": "name",
			"onError":     "drop",
		}

		res, err := registry.applyTransformation(msg, "db_lookup", config)
		if err != nil {
			t.Fatalf("Expected no error due to drop policy, got %v", err)
		}
		if res != nil {
			t.Error("Expected nil message due to drop policy")
		}
	})
}
