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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/engine/registry"
	"github.com/gsoultan/hermod/internal/storage"
)

// A new workflow is saved as version 1 when it is created. Without it, a flow
// promoted before its first edit went out as "v0": the release, the strip on
// the flow page and the approval card in production all named a version that
// does not exist.
func TestCreatingAWorkflowSavesVersionOne(t *testing.T) {
	store := newSQLiteStore(t, "workflow-create-version")
	h := &WorkflowHandler{Handler: &handlers.Handler{Storage: store, LogStorage: store, Registry: registry.NewRegistry(store)}}
	mux := http.NewServeMux()
	h.RegisterWorkflowRoutes(mux)
	if err := store.CreateSource(t.Context(), storage.Source{ID: "in", Name: "signups", Type: "webhook", Config: map[string]string{"path": "/api/webhooks/signups"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSink(t.Context(), storage.Sink{ID: "out", Name: "audit-log", Type: "stdout"}); err != nil {
		t.Fatal(err)
	}
	flow := func(name string) *strings.Reader {
		return strings.NewReader(`{"name":"` + name + `","nodes":[{"id":"s","type":"source","ref_id":"in"},{"id":"k","type":"sink","ref_id":"out"}],"edges":[{"id":"e","source_id":"s","target_id":"k"}]}`)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/workflows", flow("Onboarding")))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatalf("no id in %s (%v)", rec.Body, err)
	}
	versions, err := store.ListWorkflowVersions(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || versions[0].Version != 1 {
		t.Fatalf("versions after creating = %+v, want just version 1", versions)
	}

	// Its first update is version 2.
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/api/workflows/"+created.ID,
		flow("Onboarding, renamed")))
	if rec.Code != http.StatusOK {
		t.Fatalf("update status %d: %s", rec.Code, rec.Body)
	}
	if versions, err = store.ListWorkflowVersions(t.Context(), created.ID); err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || versions[0].Version != 2 {
		t.Fatalf("versions after the first update = %+v, want 2 then 1", versions)
	}
}
