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
	"net/http"
	"testing"

	"github.com/gsoultan/hermod/internal/environment"
	"github.com/gsoultan/hermod/internal/storage"
)

// The administrator first-run setup creates has no vhosts listed, because an
// Administrator has every one. Promoting a flow outside the default vhost used
// to answer "Forbidden: you do not have access to vhost hr" to exactly that
// person, so the promote dialog could not even run its checks.
func TestTheSetupAdministratorPromotesFromAnyVHost(t *testing.T) {
	configured(t)
	stg := newInstance(t, "Staging", environment.RoleStaging)
	prd := newInstance(t, "Production", environment.RoleProduction)
	ctx := t.Context()
	for _, c := range []struct {
		st  storage.Storage
		snk string
	}{{stg.st, "k-crm"}, {prd.st, "p-crm"}} {
		if err := c.st.CreateSink(ctx, storage.Sink{ID: c.snk, Name: "crm", Type: "http", VHost: "hr",
			Config: map[string]string{"url": "https://crm.example.com"}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := stg.st.CreateSource(ctx, storage.Source{ID: "s-orders", Name: "orders", Type: "webhook", VHost: "hr",
		Config: map[string]string{"path": "/orders", "api_key": "typed-in-plain"}}); err != nil {
		t.Fatal(err)
	}
	if err := stg.st.CreateWorkflow(ctx, storage.Workflow{ID: "wf-hr", Name: "orders to crm", VHost: "hr",
		Nodes: []storage.WorkflowNode{{ID: "n1", Type: "source", RefID: "s-orders"}, {ID: "n2", Type: "sink", RefID: "k-crm"}},
		Edges: []storage.WorkflowEdge{{ID: "e1", SourceID: "n1", TargetID: "n2"}}}); err != nil {
		t.Fatal(err)
	}
	var code struct {
		Code string `json:"code"`
	}
	prd.must(t, http.StatusCreated, "owner", http.MethodPost, "/api/environments/pairing-codes", map[string]string{"vhost": "hr"}, &code)
	var link environment.Link
	stg.must(t, http.StatusCreated, "owner", http.MethodPost, "/api/environments/links",
		map[string]string{"url": prd.srv.URL, "code": code.Code, "self_url": stg.srv.URL, "vhost": "hr"}, &link)

	release := map[string]any{"items": []any{map[string]any{"workflow_id": "wf-hr"}}}
	stg.must(t, http.StatusOK, "owner", http.MethodPost, "/api/environments/releases/preview", release, nil)
	stg.must(t, http.StatusOK, "owner", http.MethodPost, "/api/environments/links/"+link.ID+"/preview", map[string]any{"workflow_id": "wf-hr"}, nil)
	stg.must(t, http.StatusNoContent, "owner", http.MethodPost, "/api/environments/connections/source/s-orders/secret",
		map[string]string{"key": "api_key", "name": "ORDERS_API_KEY"}, nil)

	// An editor without hr is still kept out.
	if got, out := stg.call(t, "editor", http.MethodPost, "/api/environments/releases/preview", release); got != http.StatusForbidden {
		t.Fatalf("an editor without hr previewed it: %d %s", got, out)
	}
}
