package registry

import (
	"context"
	"testing"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/factory"
	"github.com/user/hermod/internal/storage"
)

type mockStatefulSource struct {
	hermod.Source
	state map[string]string
}

func (m *mockStatefulSource) Ack(ctx context.Context, msg hermod.Message) error {
	m.state = map[string]string{"last_lsn": "some-lsn"}
	return nil
}

func (m *mockStatefulSource) GetState() map[string]string {
	return m.state
}

func (m *mockStatefulSource) SetState(state map[string]string) {
	m.state = state
}

func (m *mockStatefulSource) Ping(ctx context.Context) error { return nil }
func (m *mockStatefulSource) Close() error                   { return nil }

type mockStorageForState struct {
	storage.Storage
	src storage.Source
}

func (m *mockStorageForState) GetSource(ctx context.Context, id string) (storage.Source, error) {
	return m.src, nil
}
func (m *mockStorageForState) ListWorkflows(ctx context.Context, filter storage.CommonFilter) ([]storage.Workflow, int, error) {
	return nil, 0, nil
}
func (m *mockStorageForState) UpdateSourceState(ctx context.Context, id string, state map[string]string) error {
	m.src.State = state
	return nil
}

func TestStatePersistence(t *testing.T) {
	mockSrc := &mockStatefulSource{}
	store := &mockStorageForState{
		src: storage.Source{
			ID:   "src-1",
			Type: "mock",
		},
	}

	r := NewRegistry(store)

	// Since we are in registry package, we can access unexported statefulSource
	sSource := &statefulSource{
		Source:   mockSrc,
		registry: r,
		sourceID: "src-1",
	}

	err := sSource.Ack(context.Background(), nil)
	if err != nil {
		t.Fatalf("Ack failed: %v", err)
	}

	if store.src.State["last_lsn"] != "some-lsn" {
		t.Errorf("Expected state to be persisted, got %v", store.src.State)
	}
}

func TestMSSQLSourceStateInitialization(t *testing.T) {
	// This test verifies that MSSQLSource correctly loads state from factory.SourceConfig
	cfg := factory.SourceConfig{
		ID:   "mssql-1",
		Type: "mssql",
		Config: map[string]string{
			"host":     "localhost",
			"database": "test",
		},
		State: map[string]string{
			"dbo.table1": "0000000000000001",
		},
	}

	src, err := factory.CreateSource(cfg)
	if err != nil {
		t.Fatalf("Failed to create source: %v", err)
	}

	stateful, ok := src.(hermod.Stateful)
	if !ok {
		t.Fatalf("Source should be stateful")
	}

	state := stateful.GetState()
	if state["dbo.table1"] != "0000000000000001" {
		t.Errorf("Expected state dbo.table1=0000000000000001, got %v", state)
	}
}
