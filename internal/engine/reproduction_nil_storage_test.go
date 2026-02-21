package engine

import (
	"testing"

	"github.com/user/hermod/internal/storage"
)

func TestStartWorkflow_NilStorage(t *testing.T) {
	r := NewRegistry(nil)
	wf := storage.Workflow{
		ID: "901ae2bd-6206-4ca1-9ff7-3f055b9e7c9d",
		Nodes: []storage.WorkflowNode{
			{ID: "node1", Type: "source", RefID: "src1"},
			{ID: "node2", Type: "sink", RefID: "snk1"},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "edge1", SourceID: "node1", TargetID: "node2"},
		},
	}

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Still panicking: %v", r)
		}
	}()

	err := r.StartWorkflow(wf.ID, wf)
	if err == nil {
		t.Error("Expected error when storage is nil, but got nil")
	} else {
		t.Logf("Got expected error: %v", err)
		expected := "registry storage is not initialized, cannot start workflow 901ae2bd-6206-4ca1-9ff7-3f055b9e7c9d"
		if err.Error() != expected {
			t.Errorf("Expected error message %q, got %q", expected, err.Error())
		}
	}
}

func TestRebuildWorkflow_NilStorage(t *testing.T) {
	r := NewRegistry(nil)
	err := r.RebuildWorkflow(t.Context(), "wf1", 0)
	if err == nil {
		t.Error("Expected error when storage is nil, but got nil")
	} else {
		expected := "registry storage is not initialized, cannot rebuild workflow wf1"
		if err.Error() != expected {
			t.Errorf("Expected error message %q, got %q", expected, err.Error())
		}
	}
}

func TestGetOrOpenDBByID_NilStorage(t *testing.T) {
	r := NewRegistry(nil)
	_, _, err := r.GetOrOpenDBByID(t.Context(), "src1")
	if err == nil {
		t.Error("Expected error when storage is nil, but got nil")
	} else {
		expected := "registry storage is not initialized"
		if err.Error() != expected {
			t.Errorf("Expected error message %q, got %q", expected, err.Error())
		}
	}
}

func TestGetSource_NilStorage(t *testing.T) {
	r := NewRegistry(nil)
	_, err := r.GetSource(t.Context(), "src1")
	if err == nil {
		t.Error("Expected error when storage is nil, but got nil")
	} else {
		expected := "registry storage is not initialized"
		if err.Error() != expected {
			t.Errorf("Expected error message %q, got %q", expected, err.Error())
		}
	}
}
