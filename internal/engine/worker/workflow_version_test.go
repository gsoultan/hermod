// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package worker

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

// A remote worker reads the sub-flow version a caller pins from the platform.
// Its storage adapter used to answer every version "not found", so a workflow
// calling a sub-flow could not start on a remote worker at all.
func TestAPIStorageReadsAWorkflowVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/workflows/address/versions/3" {
			_ = json.NewEncoder(w).Encode(storage.WorkflowVersion{WorkflowID: "address", Version: 3,
				Nodes: []storage.WorkflowNode{{ID: "in", Type: "subflow_input"}}})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	st := NewAPIStorage(NewWorkerAPIClient(srv.URL, "t"))

	v, err := st.GetWorkflowVersion(t.Context(), "address", 3)
	if err != nil || v.Version != 3 || len(v.Nodes) != 1 {
		t.Fatalf("GetWorkflowVersion = %+v, %v", v, err)
	}
	if _, err := st.GetWorkflowVersion(t.Context(), "address", 4); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("a missing version: err = %v, want storage.ErrNotFound", err)
	}
}
