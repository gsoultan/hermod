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
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/engine/registry"
	"github.com/gsoultan/hermod/internal/storage"
)

// A flow that is switched on but whose engine is not running here — it
// crashed, or it runs on another worker — shows Stopped with a Stop button.
// Pressing Stop answered 200 and changed nothing: the handler left the saving
// to the registry, and the registry saves only when it had an engine to stop.
// The page reloaded the flow, found it still on, and showed the same button.
func TestStoppingAFlowWithNoEngineHereTurnsItOff(t *testing.T) {
	store := &vhostScopeStorage{mockWorkspaceStorage: newMockWorkspaceStorage()}
	store.workflows["wf-auth"] = storage.Workflow{ID: "wf-auth", Name: "Auth Session Sync", Active: true, Status: "Error: source closed"}
	mux := http.NewServeMux()
	NewWorkflowHandler(&handlers.Handler{Storage: store, LogStorage: store, Registry: &registry.Registry{}}).RegisterWorkflowRoutes(mux)

	admin := &storage.User{ID: "u-admin", Role: storage.RoleAdministrator}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/workflows/wf-auth/toggle", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, admin))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Stop was answered %d, want 200: %s", rec.Code, rec.Body.String())
	}
	saved, err := store.GetWorkflow(t.Context(), "wf-auth")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Active {
		t.Errorf("after Stop the saved flow is still switched on (status %q); the page shows Stop again", saved.Status)
	}
}
