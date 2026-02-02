package schema

import (
	"context"
	"testing"

	"github.com/user/hermod/internal/storage"
)

type mockStorage struct {
	storage.Storage
	schemas map[string][]storage.Schema
}

func (m *mockStorage) CreateSchema(ctx context.Context, s storage.Schema) error {
	m.schemas[s.Name] = append(m.schemas[s.Name], s)
	return nil
}

func (m *mockStorage) GetLatestSchema(ctx context.Context, name string) (storage.Schema, error) {
	schemas, ok := m.schemas[name]
	if !ok || len(schemas) == 0 {
		return storage.Schema{}, storage.ErrNotFound
	}
	return schemas[len(schemas)-1], nil
}

func TestStorageRegistry_Register(t *testing.T) {
	mock := &mockStorage{schemas: make(map[string][]storage.Schema)}
	reg := NewStorageRegistry(mock)

	// Register version 1
	v1, err := reg.Register(context.Background(), "user", JSONSchema, `{"type": "object", "required": ["id"]}`)
	if err != nil {
		t.Fatalf("failed to register v1: %v", err)
	}
	if v1 != 1 {
		t.Errorf("expected version 1, got %d", v1)
	}

	// Register version 2 (compatible)
	v2, err := reg.Register(context.Background(), "user", JSONSchema, `{"type": "object", "required": ["id"], "properties": {"name": {"type": "string"}}}`)
	if err != nil {
		t.Fatalf("failed to register v2: %v", err)
	}
	if v2 != 2 {
		t.Errorf("expected version 2, got %d", v2)
	}

	// Register version 3 (incompatible - adding a new required field)
	_, err = reg.Register(context.Background(), "user", JSONSchema, `{"type": "object", "required": ["id", "email"]}`)
	if err == nil {
		t.Error("expected error when registering incompatible schema, got nil")
	}
}
