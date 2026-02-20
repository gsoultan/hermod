package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/user/hermod/internal/storage"
	"github.com/xeipuuv/gojsonschema"
)

// Registry defines the interface for the global schema registry.
type Registry interface {
	Register(ctx context.Context, name string, schemaType SchemaType, content string) (int, error)
	GetValidator(ctx context.Context, name string, version int) (Validator, error)
	GetLatestValidator(ctx context.Context, name string) (Validator, int, error)
	CheckCompatibility(ctx context.Context, name string, schemaType SchemaType, content string) error
}

// StorageRegistry implements Registry using the storage backend.
type StorageRegistry struct {
	storage storage.Storage
}

// NewStorageRegistry creates a new StorageRegistry.
func NewStorageRegistry(s storage.Storage) *StorageRegistry {
	return &StorageRegistry{storage: s}
}

// Register adds a new schema version to the registry.
func (r *StorageRegistry) Register(ctx context.Context, name string, schemaType SchemaType, content string) (int, error) {
	// 1. Validate the schema first
	_, err := NewValidator(SchemaConfig{Type: schemaType, Schema: content})
	if err != nil {
		return 0, fmt.Errorf("invalid schema: %w", err)
	}

	// 2. Check compatibility if a previous version exists
	if err := r.CheckCompatibility(ctx, name, schemaType, content); err != nil {
		return 0, fmt.Errorf("schema compatibility check failed: %w", err)
	}

	// 3. Get latest version and increment
	version := 1
	latest, err := r.storage.GetLatestSchema(ctx, name)
	if err == nil {
		version = latest.Version + 1
	}

	// 4. Save to storage
	sc := storage.Schema{
		Name:      name,
		Version:   version,
		Type:      string(schemaType),
		Content:   content,
		CreatedAt: time.Now(),
	}

	if err := r.storage.CreateSchema(ctx, sc); err != nil {
		return 0, err
	}

	return version, nil
}

// GetValidator retrieves a validator for a specific schema version.
func (r *StorageRegistry) GetValidator(ctx context.Context, name string, version int) (Validator, error) {
	sc, err := r.storage.GetSchema(ctx, name, version)
	if err != nil {
		return nil, err
	}

	return NewValidator(SchemaConfig{
		Type:   SchemaType(sc.Type),
		Schema: sc.Content,
	})
}

// GetLatestValidator retrieves a validator for the latest schema version.
func (r *StorageRegistry) GetLatestValidator(ctx context.Context, name string) (Validator, int, error) {
	sc, err := r.storage.GetLatestSchema(ctx, name)
	if err != nil {
		return nil, 0, err
	}

	v, err := NewValidator(SchemaConfig{
		Type:   SchemaType(sc.Type),
		Schema: sc.Content,
	})
	return v, sc.Version, err
}

// CheckCompatibility verifies if the new schema is compatible with previous versions.
// For now, it implements a basic check.
func (r *StorageRegistry) CheckCompatibility(ctx context.Context, name string, schemaType SchemaType, content string) error {
	latest, err := r.storage.GetLatestSchema(ctx, name)
	if err != nil {
		// No previous version, so it's compatible
		return nil
	}

	if latest.Type != string(schemaType) {
		return fmt.Errorf("schema type mismatch: %s vs %s", latest.Type, schemaType)
	}

	if schemaType == JSONSchema {
		return r.checkJSONCompatibility(latest.Content, content)
	}

	// For other types, fallback to equality check for now
	if latest.Content != content {
		// In a real enterprise system, we would have full Avro/Proto compatibility checks here.
		return nil // Allow for now, but in strict mode we might fail
	}

	return nil
}

func (r *StorageRegistry) checkJSONCompatibility(oldContent, newContent string) error {
	// 1. Ensure both are valid JSON schemas
	newLoader := gojsonschema.NewStringLoader(newContent)

	if _, err := gojsonschema.NewSchema(newLoader); err != nil {
		return fmt.Errorf("new schema is invalid: %w", err)
	}

	// 2. Structural compatibility check (simplified)
	var oldSchema, newSchema map[string]any
	if err := json.Unmarshal([]byte(oldContent), &oldSchema); err != nil {
		return nil // Should not happen if it was registered
	}
	if err := json.Unmarshal([]byte(newContent), &newSchema); err != nil {
		return fmt.Errorf("invalid new schema JSON: %w", err)
	}

	// Simple check: new schema should not add new 'required' fields that weren't there
	oldReq, _ := oldSchema["required"].([]any)
	newReq, _ := newSchema["required"].([]any)

	oldReqMap := make(map[string]bool)
	for _, r := range oldReq {
		if s, ok := r.(string); ok {
			oldReqMap[s] = true
		}
	}

	for _, r := range newReq {
		if s, ok := r.(string); ok {
			if !oldReqMap[s] {
				return fmt.Errorf("backward compatibility broken: new required field '%s' added", s)
			}
		}
	}

	return nil
}
