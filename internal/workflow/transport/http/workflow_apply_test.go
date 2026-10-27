// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
)

// ApplyWorkflowChange is the update path for a change nobody typed into the
// editor (an approved self-healing proposal). It must leave the same trail
// an edit does: validation, a stored workflow and a version — and a version
// of the state it replaced, so the change can always be rolled back.

type versionedStore struct {
	*mockWorkspaceStorage
	versions []storage.WorkflowVersion // newest first, as storage lists them
}

func (v *versionedStore) CreateWorkflowVersion(_ context.Context, ver storage.WorkflowVersion) error {
	v.versions = slices.Insert(v.versions, 0, ver)
	return nil
}

func (v *versionedStore) ListWorkflowVersions(_ context.Context, id string) ([]storage.WorkflowVersion, error) {
	var out []storage.WorkflowVersion
	for _, ver := range v.versions {
		if ver.WorkflowID == id {
			out = append(out, ver)
		}
	}
	return out, nil
}

func (v *versionedStore) GetWorkflowVersion(_ context.Context, id string, n int) (storage.WorkflowVersion, error) {
	for _, ver := range v.versions {
		if ver.WorkflowID == id && ver.Version == n {
			return ver, nil
		}
	}
	return storage.WorkflowVersion{}, storage.ErrNotFound
}

func newApplyFixture(t *testing.T) (*versionedStore, *WorkflowHandler, *http.ServeMux) {
	t.Helper()
	st := &versionedStore{mockWorkspaceStorage: newMockWorkspaceStorage()}
	st.sources["src-1"] = storage.Source{ID: "src-1", Name: "src", Type: "http"}
	st.sinks["snk-1"] = storage.Sink{ID: "snk-1", Name: "snk", Type: "http"}
	h := &WorkflowHandler{Handler: &handlers.Handler{Storage: st, LogStorage: st}}
	mux := http.NewServeMux()
	h.RegisterWorkflowRoutes(mux)
	return st, h, mux
}

func retried(wf storage.Workflow, n int) storage.Workflow {
	wf.MaxRetries = n
	return wf
}

func TestApplyWorkflowChangeRecordsTheOldStateWhenThereIsNoHistory(t *testing.T) {
	st, h, mux := newApplyFixture(t)
	before := retried(validWorkflow("wf", "orders", ""), 2)
	st.workflows["wf"] = before

	prev, applied, err := h.ApplyWorkflowChange(t.Context(), before, retried(before, 3), "ada", "Self-healing: retries")
	if err != nil {
		t.Fatal(err)
	}
	if prev != 1 || applied != 2 || len(st.versions) != 2 {
		t.Fatalf("prev %d applied %d versions %d", prev, applied, len(st.versions))
	}
	if st.workflows["wf"].MaxRetries != 3 {
		t.Fatalf("stored = %+v", st.workflows["wf"])
	}
	if v := st.versions[0]; v.Version != 2 || v.CreatedBy != "ada" || v.Message != "Self-healing: retries" {
		t.Fatalf("applied version = %+v", v)
	}

	// The change rolls back through the ordinary rollback route.
	rr := do(t, mux, http.MethodPost, "/api/workflows/wf/rollback/1", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("rollback %d: %s", rr.Code, rr.Body.String())
	}
	if st.workflows["wf"].MaxRetries != 2 {
		t.Fatalf("after rollback = %+v", st.workflows["wf"])
	}
}

func TestApplyWorkflowChangeAddsOneVersionToExistingHistory(t *testing.T) {
	st, h, _ := newApplyFixture(t)
	before := retried(validWorkflow("wf", "orders", ""), 2)
	st.workflows["wf"] = before
	cfg, _ := json.Marshal(before)
	st.versions = []storage.WorkflowVersion{{WorkflowID: "wf", Version: 4, Config: string(cfg)}}

	prev, applied, err := h.ApplyWorkflowChange(t.Context(), before, retried(before, 3), "ada", "m")
	if err != nil || prev != 4 || applied != 5 || len(st.versions) != 2 {
		t.Fatalf("prev %d applied %d versions %d err %v", prev, applied, len(st.versions), err)
	}
}

func TestApplyWorkflowChangeRefusesAnInvalidWorkflow(t *testing.T) {
	st, h, _ := newApplyFixture(t)
	before := validWorkflow("wf", "orders", "")
	st.workflows["wf"] = before
	broken := before
	broken.Nodes = nil
	broken.Edges = nil

	_, _, err := h.ApplyWorkflowChange(t.Context(), before, broken, "ada", "m")
	if !errors.Is(err, ErrInvalidWorkflow) {
		t.Fatalf("err = %v", err)
	}
	if len(st.workflows["wf"].Nodes) != 2 || len(st.versions) != 0 {
		t.Fatal("an invalid change was stored")
	}
}

// A promotion from another environment arrives through these too: a new
// workflow is created as version 1, and a rollback restores a version and
// records the restore.

func TestCreateWorkflowVersionedStoresVersionOne(t *testing.T) {
	st, h, _ := newApplyFixture(t)
	wf := validWorkflow("wf-new", "orders", "")

	applied, err := h.CreateWorkflowVersioned(t.Context(), wf, "rina", "Promoted from Staging version 3")
	if err != nil || applied != 1 {
		t.Fatalf("applied %d err %v", applied, err)
	}
	if _, ok := st.workflows["wf-new"]; !ok {
		t.Fatal("the workflow was not stored")
	}
	if len(st.versions) != 1 || st.versions[0].Version != 1 || st.versions[0].CreatedBy != "rina" {
		t.Fatalf("versions = %+v", st.versions)
	}

	broken := validWorkflow("wf-bad", "orders", "")
	broken.Nodes = nil
	if _, err := h.CreateWorkflowVersioned(t.Context(), broken, "rina", "m"); !errors.Is(err, ErrInvalidWorkflow) {
		t.Fatalf("invalid: err = %v", err)
	}
	if _, ok := st.workflows["wf-bad"]; ok {
		t.Fatal("an invalid workflow was stored")
	}
}

func TestRollbackWorkflowToRestoresAndRecords(t *testing.T) {
	st, h, _ := newApplyFixture(t)
	before := retried(validWorkflow("wf", "orders", ""), 2)
	st.workflows["wf"] = before
	if _, _, err := h.ApplyWorkflowChange(t.Context(), before, retried(before, 7), "ada", "m"); err != nil {
		t.Fatal(err)
	}
	applied, err := h.RollbackWorkflowTo(t.Context(), "wf", 1, "rina")
	if err != nil || applied != 3 {
		t.Fatalf("applied %d err %v", applied, err)
	}
	if st.workflows["wf"].MaxRetries != 2 || st.versions[0].Message != "Rolled back to version 1" {
		t.Fatalf("stored %+v, newest version %+v", st.workflows["wf"], st.versions[0])
	}
	if _, err := h.RollbackWorkflowTo(t.Context(), "wf", 42, "rina"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("unknown version: err = %v", err)
	}
}

func TestPortableDropsRuntimeColumns(t *testing.T) {
	_, h, _ := newApplyFixture(t)
	wf := validWorkflow("wf", "orders", "")
	wf.WorkerID, wf.Status, wf.TotalErrors = "w1", "Running", 4
	got := h.Portable(wf)
	if got.WorkerID != "" || got.Status != "" || got.TotalErrors != 0 || got.ID != "wf" {
		t.Fatalf("Portable = %+v", got)
	}
	if err := h.Validate(t.Context(), got); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}
