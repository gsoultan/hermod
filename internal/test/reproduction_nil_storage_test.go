// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package test

import (
	"testing"

	"github.com/gsoultan/hermod/internal/engine/registry"

	"github.com/gsoultan/hermod/internal/storage"
)

func TestStartWorkflow_NilStorage(t *testing.T) {
	r := registry.NewRegistry(nil)
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
	r := registry.NewRegistry(nil)
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
	r := registry.NewRegistry(nil)
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
	r := registry.NewRegistry(nil)
	_, err := r.GetSourceConfig(t.Context(), "src1")
	if err == nil {
		t.Error("Expected error when storage is nil, but got nil")
	} else {
		expected := "registry storage is not initialized"
		if err.Error() != expected {
			t.Errorf("Expected error message %q, got %q", expected, err.Error())
		}
	}
}
