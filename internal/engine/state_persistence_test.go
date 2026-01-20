package engine

import (
	"context"
	"testing"

	"github.com/user/hermod"
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

func TestStatePersistence(t *testing.T) {
	mockSrc := &mockStatefulSource{}
	store := &mockStorageForInterval{
		src: storage.Source{
			ID:   "src-1",
			Type: "mock",
		},
		wf: storage.Workflow{
			ID: "wf-1",
			Nodes: []storage.WorkflowNode{
				{ID: "node-1", Type: "source", RefID: "src-1"},
				{ID: "node-2", Type: "sink", RefID: "snk-1"},
			},
			Edges: []storage.WorkflowEdge{
				{ID: "edge-1", SourceID: "node-1", TargetID: "node-2"},
			},
			Active: true,
		},
	}

	r := NewRegistry(store)

	// Since we know StartWorkflow wraps it in statefulSource
	// We can manually call Ack on a statefulSource and check if it updates the storage.
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
	// This test verifies that MSSQLSource correctly loads state from SourceConfig
	cfg := SourceConfig{
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

	src, err := CreateSource(cfg)
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
